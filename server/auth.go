package server

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/deluan/rest"
	"github.com/go-chi/jwtauth/v5"
	"github.com/google/uuid"
	"github.com/go-ldap/ldap"
	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/consts"
	"github.com/navidrome/navidrome/core/auth"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/request"
	"github.com/navidrome/navidrome/utils/gravatar"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var (
	ErrNoUsers         = errors.New("no users created")
	ErrUnauthenticated = errors.New("request not authenticated")
)

func login(ds model.DataStore) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		username, password, err := getCredentialsFromBody(r)
		if err != nil {
			log.Error(r, "Parsing request body", err)
			_ = rest.RespondWithError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		doLogin(ds, username, password, w, r)
	}
}

func doLogin(ds model.DataStore, username string, password string, w http.ResponseWriter, r *http.Request) {
	user, err := validateLogin(ds.User(r.Context()), username, password)
	if err != nil {
		_ = rest.RespondWithError(w, http.StatusInternalServerError, "Unknown error authentication user. Please try again")
		return
	}
	if user == nil {
		log.Warn(r, "Unsuccessful login", "username", username, "request", r.Header)
		_ = rest.RespondWithError(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}

	tokenString, err := auth.CreateToken(user)
	if err != nil {
		_ = rest.RespondWithError(w, http.StatusInternalServerError, "Unknown error authenticating user. Please try again")
		return
	}
	payload := buildAuthPayload(user)
	payload["token"] = tokenString
	_ = rest.RespondWithJSON(w, http.StatusOK, payload)
}

func buildAuthPayload(user *model.User) map[string]interface{} {
	payload := map[string]interface{}{
		"id":       user.ID,
		"name":     user.Name,
		"username": user.UserName,
		"isAdmin":  user.IsAdmin,
	}
	if conf.Server.EnableGravatar && user.Email != "" {
		payload["avatar"] = gravatar.Url(user.Email, 50)
	}

	bytes := make([]byte, 3)
	_, err := rand.Read(bytes)
	if err != nil {
		log.Error("Could not create subsonic salt", "user", user.UserName, err)
		return payload
	}
	subsonicSalt := hex.EncodeToString(bytes)
	payload["subsonicSalt"] = subsonicSalt

	subsonicToken := md5.Sum([]byte(user.Password + subsonicSalt))
	payload["subsonicToken"] = hex.EncodeToString(subsonicToken[:])

	return payload
}

func getCredentialsFromBody(r *http.Request) (username string, password string, err error) {
	data := make(map[string]string)
	decoder := json.NewDecoder(r.Body)
	if err = decoder.Decode(&data); err != nil {
		log.Error(r, "parsing request body", err)
		err = errors.New("invalid request payload")
		return
	}
	username = data["username"]
	password = data["password"]
	return username, password, nil
}

func createAdmin(ds model.DataStore) func(w http.ResponseWriter, r *http.Request) {
	auth.Init(ds)

	return func(w http.ResponseWriter, r *http.Request) {
		username, password, err := getCredentialsFromBody(r)
		if err != nil {
			log.Error(r, "parsing request body", err)
			_ = rest.RespondWithError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		c, err := ds.User(r.Context()).CountAll()
		if err != nil {
			_ = rest.RespondWithError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if c > 0 {
			_ = rest.RespondWithError(w, http.StatusForbidden, "Cannot create another first admin")
			return
		}
		err = createAdminUser(r.Context(), ds, username, password)
		if err != nil {
			_ = rest.RespondWithError(w, http.StatusInternalServerError, err.Error())
			return
		}
		doLogin(ds, username, password, w, r)
	}
}

func createAdminUser(ctx context.Context, ds model.DataStore, username, password string) error {
	log.Warn("Creating initial user", "user", username)
	now := time.Now()
	caser := cases.Title(language.Und)
	initialUser := model.User{
		ID:          uuid.NewString(),
		UserName:    username,
		Name:        caser.String(username),
		Email:       "",
		NewPassword: password,
		IsAdmin:     true,
		LastLoginAt: now,
	}
	err := ds.User(ctx).Put(&initialUser)
	if err != nil {
		log.Error("Could not create initial user", "user", initialUser, err)
	}
	return nil
}

func validateLogin(userRepo model.UserRepository, userName, password string) (*model.User, error) {
	u, err := validateLoginLDAP(userRepo, userName, password)
	if u != nil && err == nil {
		return u, nil
	}
	u, err = userRepo.FindByUsernameWithPassword(userName)
	if errors.Is(err, model.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if u.Password != password {
		return nil, nil
	}
	err = userRepo.UpdateLastLoginAt(u.ID)
	if err != nil {
		log.Error("Could not update LastLoginAt", "user", userName)
	}
	return u, nil
}

// This method maps the custom authorization header to the default 'Authorization', used by the jwtauth library
func authHeaderMapper(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := r.Header.Get(consts.UIAuthorizationHeader)
		r.Header.Set("Authorization", bearer)
		next.ServeHTTP(w, r)
	})
}

func jwtVerifier(next http.Handler) http.Handler {
	return jwtauth.Verify(auth.TokenAuth, jwtauth.TokenFromHeader, jwtauth.TokenFromCookie, jwtauth.TokenFromQuery)(next)
}

func UsernameFromToken(r *http.Request) string {
	token, claims, err := jwtauth.FromContext(r.Context())
	if err != nil || claims["sub"] == nil || token == nil {
		return ""
	}
	log.Trace(r, "Found username in JWT token", "username", token.Subject())
	return token.Subject()
}

func UsernameFromReverseProxyHeader(r *http.Request) string {
	if conf.Server.ReverseProxyWhitelist == "" {
		return ""
	}
	if !validateIPAgainstList(r.RemoteAddr, conf.Server.ReverseProxyWhitelist) {
		log.Warn("IP is not whitelisted for reverse proxy login", "ip", r.RemoteAddr)
		return ""
	}
	username := r.Header.Get(conf.Server.ReverseProxyUserHeader)
	if username == "" {
		return ""
	}
	log.Trace(r, "Found username in ReverseProxyUserHeader", "username", username)
	return username
}

func UsernameFromConfig(r *http.Request) string {
	return conf.Server.DevAutoLoginUsername
}


func validateLoginLDAP(userRepo model.UserRepository, userName, password string) (*model.User, error) {
	binduserdn := conf.Server.LDAP.BindDN
	bindpassword := conf.Server.LDAP.BindPassword
	mailAttr := conf.Server.LDAP.Mail
	nameAttr := conf.Server.LDAP.Name

	l, err := ldap.DialURL(conf.Server.LDAP.Host)
	if err != nil {
		log.Error(err)
	}
	defer l.Close()

	// First bind with a read only user
	err = l.Bind(binduserdn, bindpassword)
	if err != nil {
		log.Error(err)
	}

	// Search for the given username
	searchRequest := ldap.NewSearchRequest(
		conf.Server.LDAP.Base,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf(conf.Server.LDAP.SearchFilter, ldap.EscapeFilter(userName)),
		[]string{"dn", nameAttr, mailAttr},
		nil,
	)

	sr, err := l.Search(searchRequest)
	if err != nil {
		log.Error(err)
	}

	if len(sr.Entries) != 1 {
		log.Error("User does not exist or too many entries returned")
		return nil, nil
	}

	dn := sr.Entries[0].DN
	mail := sr.Entries[0].GetAttributeValue(mailAttr)
	name := sr.Entries[0].GetAttributeValue(nameAttr)

	authenticated := true
	// Bind as the user to verify their password
	err = l.Bind(dn, password)

	if err != nil {
		log.Error(err)
		authenticated = false
	}

	// Rebind as the read only user for any further queries
	err = l.Bind(binduserdn, bindpassword)
	if err != nil {
		log.Error(err)
	}

	if !authenticated {
		return nil, nil
	}

	u, err := userRepo.FindByUsername(userName)
	if err == model.ErrNotFound {
		u = &model.User{UserName: userName}
	}
	u.Name = name
	u.Email = mail
	u.Password = password
	err = userRepo.Put(u)
	if err != nil {
		log.Error("Could not update User", "user", userName)
	}

	err = userRepo.UpdateLastLoginAt(u.ID)
	if err != nil {
		log.Error("Could not update LastLoginAt", "user", userName)
	}

	return u, nil
}


func contextWithUser(ctx context.Context, ds model.DataStore, username string) (context.Context, error) {
	user, err := ds.User(ctx).FindByUsername(username)
	if err == nil {
		ctx = request.WithUsername(ctx, user.UserName)
		return request.WithUser(ctx, *user), nil
	}
	log.Error(ctx, "Authenticated username not found in DB", "username", username)
	return ctx, err
}

func authenticateRequest(ds model.DataStore, r *http.Request, findUsernameFns ...func(r *http.Request) string) (context.Context, error) {
	var username string
	for _, fn := range findUsernameFns {
		username = fn(r)
		if username != "" {
			break
		}
	}
	if username == "" {
		return nil, ErrUnauthenticated
	}

	return contextWithUser(r.Context(), ds, username)
}

func Authenticator(ds model.DataStore) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, err := authenticateRequest(ds, r, UsernameFromConfig, UsernameFromToken, UsernameFromReverseProxyHeader)
			if err != nil {
				_ = rest.RespondWithError(w, http.StatusUnauthorized, "Not authenticated")
				return
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// JWTRefresher updates the expire date of the received JWT token, and add the new one to the Authorization Header
func JWTRefresher(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		token, _, err := jwtauth.FromContext(ctx)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		newTokenString, err := auth.TouchToken(token)
		if err != nil {
			log.Error(r, "Could not sign new token", err)
			_ = rest.RespondWithError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		w.Header().Set(consts.UIAuthorizationHeader, newTokenString)
		next.ServeHTTP(w, r)
	})
}

func handleLoginFromHeaders(ds model.DataStore, r *http.Request) map[string]interface{} {
	username := UsernameFromConfig(r)
	if username == "" {
		username = UsernameFromReverseProxyHeader(r)
		if username == "" {
			return nil
		}
	}

	userRepo := ds.User(r.Context())
	user, err := userRepo.FindByUsernameWithPassword(username)
	if user == nil || err != nil {
		log.Warn(r, "User passed in header not found", "user", username)
		return nil
	}

	err = userRepo.UpdateLastLoginAt(user.ID)
	if err != nil {
		log.Error(r, "Could not update LastLoginAt", "user", username, err)
		return nil
	}

	return buildAuthPayload(user)
}

func validateIPAgainstList(ip string, comaSeparatedList string) bool {
	if comaSeparatedList == "" || ip == "" {
		return false
	}

	if net.ParseIP(ip) == nil {
		ip, _, _ = net.SplitHostPort(ip)
	}

	if ip == "" {
		return false
	}

	cidrs := strings.Split(comaSeparatedList, ",")
	testedIP, _, err := net.ParseCIDR(fmt.Sprintf("%s/32", ip))

	if err != nil {
		return false
	}

	for _, cidr := range cidrs {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err == nil && ipnet.Contains(testedIP) {
			return true
		}
	}

	return false
}
