package sshc

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/lfdominguez/desk/internal/sshconfig"
)

// implemented is what x/crypto can do per ssh_config keyword, legacy
// algorithms included: they are offered only when the config lists them
// (KexAlgorithms +diffie-hellman-group1-sha1…), as ssh does.
func implemented() map[string][]string {
	s, i := ssh.SupportedAlgorithms(), ssh.InsecureAlgorithms()
	return map[string][]string{
		"kexalgorithms":     slices.Concat(s.KeyExchanges, i.KeyExchanges),
		"ciphers":           slices.Concat(s.Ciphers, i.Ciphers),
		"macs":              slices.Concat(s.MACs, i.MACs),
		"hostkeyalgorithms": slices.Concat(s.HostKeys, i.HostKeys),
	}
}

// clientConfig is the config's algorithms in x/crypto's terms: each list
// as resolved, less what x/crypto lacks (sntrup761, umac…), host key
// algorithms put in the order ssh's order_hostkeyalgs gives.
func clientConfig(cfg *sshconfig.Config, hc *hostCheck) (*ssh.ClientConfig, error) {
	impl := implemented()
	pick := func(keyword string) ([]string, error) {
		var out []string
		for _, a := range cfg.Algorithms(keyword) {
			if slices.Contains(impl[keyword], a) {
				out = append(out, a)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%s: desk implements none of %s", keyword, cfg.Get(keyword))
		}
		return out, nil
	}
	conf := &ssh.ClientConfig{User: cfg.User()}
	var err error
	if conf.KeyExchanges, err = pick("kexalgorithms"); err != nil {
		return nil, err
	}
	if conf.Ciphers, err = pick("ciphers"); err != nil {
		return nil, err
	}
	if conf.MACs, err = pick("macs"); err != nil {
		return nil, err
	}
	hk, err := pick("hostkeyalgorithms")
	if err != nil {
		return nil, err
	}
	conf.HostKeyAlgorithms = hc.order(hk)
	// RekeyLimit's data part; ponytail: its time part is dropped, x/crypto
	// rekeys by data only.
	if f := strings.Fields(cfg.Get("rekeylimit")); len(f) > 0 {
		n, _ := strconv.ParseUint(f[0], 10, 64)
		conf.RekeyThreshold = n
	}
	if v := cfg.Get("versionaddendum"); v != "" && v != "none" {
		conf.ClientVersion = "SSH-2.0-Go " + v
	}
	return conf, nil
}

// NegotiationError is a server and desk agreeing on no algorithm of a kind:
// What is the ssh_config keyword that would let one in (KexAlgorithms,
// HostKeyAlgorithms, Ciphers, MACs), Ours what desk offered, Theirs what
// the server did.
type NegotiationError struct {
	What         string
	Ours, Theirs []string
}

func (e *NegotiationError) Error() string {
	return fmt.Sprintf("Unable to negotiate: no matching %s found. Their offer: %s", e.What, strings.Join(e.Theirs, ","))
}

// Fixable is what the server offers that desk implements: the algorithms a
// "What +alg" line would make work.
func (e *NegotiationError) Fixable() []string {
	impl := implemented()[strings.ToLower(e.What)]
	var out []string
	for _, a := range e.Theirs {
		if slices.Contains(impl, a) {
			out = append(out, a)
		}
	}
	return out
}

// negotiation turns x/crypto's negotiation failure into a *NegotiationError;
// other errors pass.
func negotiation(err error) error {
	var ane *ssh.AlgorithmNegotiationError
	if !errors.As(err, &ane) {
		return err
	}
	what := "PubkeyAcceptedAlgorithms"
	switch w := ane.What; {
	case w == "key exchange":
		what = "KexAlgorithms"
	case w == "host key":
		what = "HostKeyAlgorithms"
	case strings.HasSuffix(w, "cipher"):
		what = "Ciphers"
	case strings.HasSuffix(w, "MAC"):
		what = "MACs"
	}
	return &NegotiationError{What: what, Ours: noPseudo(ane.SupportedAlgorithms), Theirs: noPseudo(ane.RequestedAlgorithms)}
}

// noPseudo drops the pseudo-algorithms of the kex list (ext-info, strict kex).
func noPseudo(l []string) []string {
	return slices.DeleteFunc(slices.Clone(l), func(a string) bool {
		return strings.HasPrefix(a, "ext-info-") || strings.HasPrefix(a, "kex-strict-")
	})
}
