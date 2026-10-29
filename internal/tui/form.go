package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fiandev/bare-eye/internal/config"
)

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldAuth
	fieldBool
	fieldPassword
)

type formField struct {
	key   string
	label string
	kind  fieldKind
	value string
	hint  string
}

type formState struct {
	fields     []formField
	focus      int
	isNew      bool
	originalID string
	password   string
	hasStored  bool
	status     string
}

func newForm(cfg *config.Config, secrets *config.Secrets, srv *config.ServerConfig) *formState {
	f := &formState{isNew: srv == nil}
	port := "22"
	user := cfg.SSH.DefaultUser
	keyPath := cfg.SSH.DefaultKey
	auth := config.AuthKey
	enabled := "yes"
	name, host, tags := "", "", ""
	if srv != nil {
		f.originalID = srv.ID
		name, host = srv.Name, srv.Host
		port = strconv.Itoa(srv.Port)
		user = srv.User
		auth = srv.Auth.Method
		if srv.Auth.KeyPath != "" {
			keyPath = srv.Auth.KeyPath
		}
		if !srv.Enabled {
			enabled = "no"
		}
		tags = strings.Join(srv.Tags, ",")
		if secrets != nil {
			if _, ok := secrets.Get(srv.ID); ok {
				f.hasStored = true
			}
		}
	}
	f.fields = []formField{
		{key: "name", label: "Name", kind: fieldText, value: name, hint: "friendly label"},
		{key: "host", label: "Host", kind: fieldText, value: host, hint: "hostname or IP"},
		{key: "port", label: "Port", kind: fieldText, value: port, hint: "default 22"},
		{key: "user", label: "User", kind: fieldText, value: user, hint: "SSH username"},
		{key: "auth", label: "Auth", kind: fieldAuth, value: auth, hint: "key / agent / password"},
		{key: "key", label: "Key path", kind: fieldText, value: keyPath, hint: "used for key auth"},
		{key: "password", label: "Password", kind: fieldPassword, value: "", hint: "stored encrypted; used for password auth"},
		{key: "tags", label: "Tags", kind: fieldText, value: tags, hint: "comma separated (optional)"},
		{key: "enabled", label: "Enabled", kind: fieldBool, value: enabled, hint: "yes / no"},
	}
	return f
}

func (f *formState) field(key string) *formField {
	for i := range f.fields {
		if f.fields[i].key == key {
			return &f.fields[i]
		}
	}
	return nil
}

func (f *formState) next(delta int) {
	f.focus = (f.focus + delta + len(f.fields)) % len(f.fields)
}

func (f *formState) adjust(delta int) {
	fl := &f.fields[f.focus]
	switch fl.kind {
	case fieldAuth:
		opts := []string{config.AuthKey, config.AuthAgent, config.AuthPassword}
		idx := 0
		for i, o := range opts {
			if o == fl.value {
				idx = i
			}
		}
		idx = (idx + delta + len(opts)) % len(opts)
		fl.value = opts[idx]
	case fieldBool:
		if fl.value == "yes" {
			fl.value = "no"
		} else {
			fl.value = "yes"
		}
	}
}

func (f *formState) insert(s string) {
	fl := &f.fields[f.focus]
	if fl.kind == fieldAuth || fl.kind == fieldBool {
		return
	}
	if fl.kind == fieldPassword {
		f.password += s
		return
	}
	fl.value += s
}

func (f *formState) backspace() {
	fl := &f.fields[f.focus]
	if fl.kind == fieldAuth || fl.kind == fieldBool {
		return
	}
	if fl.kind == fieldPassword {
		if r := []rune(f.password); len(r) > 0 {
			f.password = string(r[:len(r)-1])
		}
		return
	}
	r := []rune(fl.value)
	if len(r) > 0 {
		fl.value = string(r[:len(r)-1])
	}
}

func (f *formState) build(cfg *config.Config) (config.ServerConfig, error) {
	get := func(key string) string {
		if fl := f.field(key); fl != nil {
			return strings.TrimSpace(fl.value)
		}
		return ""
	}
	port, err := strconv.Atoi(get("port"))
	if err != nil {
		return config.ServerConfig{}, fmt.Errorf("port must be a number")
	}
	id := f.originalID
	if id == "" {
		id = config.Slug(get("name"))
		if strings.TrimSpace(get("name")) == "" {
			id = config.Slug(get("host"))
		}
	}
	srv := config.ServerConfig{
		ID:      id,
		Name:    get("name"),
		Host:    get("host"),
		Port:    port,
		User:    get("user"),
		Auth:    config.AuthConfig{Method: get("auth")},
		Enabled: get("enabled") == "yes",
	}
	if srv.Auth.Method == config.AuthKey {
		srv.Auth.KeyPath = get("key")
		if srv.Auth.KeyPath == "" {
			srv.Auth.KeyPath = cfg.SSH.DefaultKey
		}
	}
	if tags := get("tags"); tags != "" {
		for _, t := range strings.Split(tags, ",") {
			if t = strings.TrimSpace(t); t != "" {
				srv.Tags = append(srv.Tags, t)
			}
		}
	}
	if srv.Name == "" {
		srv.Name = srv.Host
	}
	if err := srv.Validate(); err != nil {
		return config.ServerConfig{}, err
	}
	return srv, nil
}

func (f *formState) render() string {
	var b strings.Builder
	b.WriteString("\n")
	for i := range f.fields {
		fl := &f.fields[i]
		marker := "  "
		if i == f.focus {
			marker = "▸ "
		}
		var val string
		switch fl.kind {
		case fieldPassword:
			val = strings.Repeat("•", len([]rune(f.password)))
			if val == "" && f.hasStored {
				val = "(stored)"
			}
		case fieldAuth:
			val = "< " + fl.value + " >"
		case fieldBool:
			val = "< " + fl.value + " >"
		default:
			val = "[" + fl.value + "]"
		}
		hint := fl.hint
		if fl.key == "password" && f.field("auth") != nil && f.field("auth").value != config.AuthPassword {
			hint = "only used with password auth"
		}
		fmt.Fprintf(&b, "  %s%-9s %-38s %s\n", marker, fl.label, truncate(val, 38), hint)
	}
	b.WriteString("\n  Tab/↑↓ move · ←/→ change · Ctrl+T test · Enter save · Esc cancel\n")
	if f.status != "" {
		b.WriteString("\n  " + f.status + "\n")
	}
	return b.String()
}
