package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/tommy/pkg/cst"
	"code.linenisgreat.com/tommy/pkg/document"
)

// newsblurTokenSource reads the plugin's own config section, the
// wrapper-stripped `config_toml` cutting-garden hands `traversal-serve` at
// initialize, and returns where the NewsBlur token lives. The section uses the
// same account shape the caldav and fastmail plugins do, so credentials are
// named, never held:
//
//	[[nebulous.accounts]]      # [[accounts]] once the section name is stripped
//	name = "newsblur"
//	password_source = "piggy"  # or "env"
//	password_key = "newsblur-api-token"   # optional for piggy: defaults to tokenPassEntry()
//
// A nil source with a nil error means the section holds no account: the plugin
// stays read-only. The source is only called when a mutation first needs the
// token, never at initialize.
func newsblurTokenSource(configTOML string) (func(context.Context) (string, error), error) {
	if strings.TrimSpace(configTOML) == "" {
		return nil, nil
	}

	doc, err := document.Parse([]byte(configTOML))
	if err != nil {
		return nil, fmt.Errorf("nebulous config: %w", err)
	}
	model, err := cst.Decompose(doc.Root())
	if err != nil {
		return nil, fmt.Errorf("nebulous config: %w", err)
	}
	var section config_common.AccountsSection
	if err := config_common.DecodeAccountsSectionInto(&section, model); err != nil {
		return nil, fmt.Errorf("nebulous config: %w", err)
	}

	switch len(section.Accounts) {
	case 0:
		return nil, nil
	case 1:
	default:
		return nil, errors.New("nebulous config: at most one account (a NewsBlur login) is supported")
	}

	account := section.Accounts[0]
	if account.Name == "" {
		return nil, errors.New("nebulous config: accounts[0] needs a name")
	}
	if account.URL != "" {
		return nil, fmt.Errorf("nebulous config: accounts[%q]: url is not used (the NewsBlur host is fixed)", account.Name)
	}
	// A piggy account that names no key takes the entry every other path uses.
	if account.PasswordSource == "piggy" && account.PasswordKey == "" {
		account.PasswordKey = tokenPassEntry()
	}
	if err := account.ValidatePassword(); err != nil {
		return nil, fmt.Errorf("nebulous config: accounts[%q]: %w", account.Name, err)
	}
	if account.PasswordEnv == "" && account.PasswordSource == "" {
		return nil, fmt.Errorf("nebulous config: accounts[%q]: set password_source and password_key", account.Name)
	}

	return func(ctx context.Context) (string, error) {
		return passwordWithin(ctx, account)
	}, nil
}

// passwordWithin resolves account's password but gives up when ctx ends or
// piggyLookupTimeout passes: Account.Password takes no context, and a locked
// piggy agent can block it indefinitely. The abandoned lookup finishes in its
// own goroutine.
func passwordWithin(ctx context.Context, account config_common.Account) (string, error) {
	type result struct {
		password string
		err      error
	}
	done := make(chan result, 1)
	go func() {
		password, err := account.Password()
		done <- result{password, err}
	}()

	ctx, cancel := context.WithTimeout(ctx, piggyLookupTimeout)
	defer cancel()
	select {
	case r := <-done:
		return strings.TrimSpace(r.password), r.err
	case <-ctx.Done():
		return "", fmt.Errorf("credential lookup for accounts[%q]: %w", account.Name, ctx.Err())
	}
}
