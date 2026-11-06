package sshconfig

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
)

// errMissingEnv is a ${VAR} that isn't set: OpenSSH returns no string then,
// which most callers treat as an error and Include as a path to skip.
var errMissingEnv = errors.New("environment variable not set")

// Expand replaces the %-tokens in s from tokens (%% is a %, an unknown token
// an error) and, with env, ${VAR} from the environment, as OpenSSH's
// percent_dollar_expand does. Config.Tokens gives the connection's tokens;
// add the ones only known at connect time (%T for LocalCommand…) to them.
func Expand(s string, tokens map[byte]string, env bool) (string, error) {
	return expand(s, tokens, env, true)
}

// expand is misc.c's vdollar_percent_expand.
func expand(s string, tokens map[byte]string, dollar, percent bool) (string, error) {
	var b strings.Builder
	missing := false
	for i := 0; i < len(s); i++ {
		if dollar && strings.HasPrefix(s[i:], "${") {
			end := strings.IndexByte(s[i+2:], '}')
			switch {
			case end < 0:
				return "", fmt.Errorf("environment variable '%s' missing closing '}'", s[i+2:])
			case end == 0:
				return "", errors.New("zero-length environment variable")
			}
			if v, ok := os.LookupEnv(s[i+2 : i+2+end]); ok {
				b.WriteString(v)
			} else {
				missing = true
			}
			i += 2 + end
			continue
		}
		if s[i] != '%' || !percent {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch {
		case i == len(s):
			return "", errors.New("invalid format")
		case s[i] == '%':
			b.WriteByte('%')
			continue
		}
		v, ok := tokens[s[i]]
		if !ok {
			return "", fmt.Errorf("unknown key %%%c", s[i])
		}
		b.WriteString(v)
	}
	if missing {
		return "", errMissingEnv
	}
	return b.String(), nil
}

// lookupUser finds ~user's home; a var for tests.
var lookupUser = func(name string) (string, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}

// tilde is misc.c's tilde_expand: ~ and ~/x from home, ~user/x from that user's.
func tilde(path, home string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~")
	if !ok {
		return path, nil
	}
	if name, after, _ := strings.Cut(rest, "/"); name != "" {
		h, err := lookupUser(name)
		if err != nil {
			return "", fmt.Errorf("no such user %s", name)
		}
		home, rest = h, after
	}
	rest = strings.TrimLeft(rest, "/")
	if !strings.HasSuffix(home, "/") {
		home += "/"
	}
	return home + rest, nil
}
