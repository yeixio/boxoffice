package store

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL addresses as people paste them. A password is often pasted as
// it is, with characters such as @, /, ?, # or % that a URL needs written as
// %40, %2F and so on; read strictly, part of the password then reads as the
// host, the port or the database, and Box Office would connect to some other
// database than the one named (PostgreSQL's default is the user's name).
// CleanDSN writes such a password the way a URL needs it, so the database
// named after the last "/" is the one used.

var keyValueDSN = regexp.MustCompile(`(^|\s)(host|dbname|user)\s*=`)

// isKeyValue says whether dsn is libpq's key=value form, such as
// "host=tower user=boxoffice dbname=boxoffice".
func isKeyValue(dsn string) bool {
	return !strings.Contains(dsn, "://") && keyValueDSN.MatchString(dsn)
}

// CleanDSN tidies a database address: spaces around it, and a PostgreSQL
// URL's password written so that it reads back as typed. Anything else is
// returned as it was.
func CleanDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	scheme, rest, ok := strings.Cut(dsn, "://")
	if !ok || (scheme != "postgres" && scheme != "postgresql") {
		return dsn
	}
	// The host comes after the last @ before the database's path, and the
	// password may itself have @ and / in it, so look for the last @ that
	// has a host:port/database after it.
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return strings.TrimRight(dsn, "/")
	}
	userinfo, hostpart := rest[:at], rest[at+1:]
	user, pass, hasPass := strings.Cut(userinfo, ":")
	if u, err := url.PathUnescape(user); err == nil {
		user = u
	}
	info := url.User(user)
	if hasPass {
		// A password already written for a URL (%40 for @) is read as
		// that; one with a % that isn't such an escape is taken as typed.
		if p, err := url.PathUnescape(pass); err == nil {
			pass = p
		}
		info = url.UserPassword(user, pass)
	}
	// A slash after the database's name isn't part of it.
	path, query, hasQuery := strings.Cut(hostpart, "?")
	path = strings.TrimRight(path, "/")
	out := scheme + "://" + info.String() + "@" + path
	if hasQuery {
		out += "?" + query
	}
	return out
}

// DatabaseName is the database a PostgreSQL address names, as PostgreSQL
// will be asked for it ("" when it can't be read).
func DatabaseName(dsn string) string {
	c, err := pgconn.ParseConfig(CleanDSN(dsn))
	if err != nil {
		return ""
	}
	if c.Database == "" {
		return c.User
	}
	return c.Database
}
