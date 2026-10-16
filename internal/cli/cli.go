// Package cli implements the artifikctl command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/keystore"
	"github.com/langtind/ctlkit/session"

	"github.com/langtind/artifikctl/internal/artifik"
)

const (
	// Name is the program name. A rename also touches the module path, the
	// skill directory with its embed line in skill_embed.go, and the docs.
	Name = "artifikctl"

	summary = "search public procurements at Artifik, read them and download their attachments"

	// envToken names the variable that overrides the keyring with the value
	// of the artifik-session cookie. Cron jobs and containers have no keyring
	// to unlock. A cookie from the environment is used as it is and never
	// renewed.
	envToken = "ARTIFIKCTL_TOKEN"
)

// App holds what the commands work with. Tests replace the fields to point
// artifikctl at a fake service and at a keyring in memory.
type App struct {
	Client *artifik.Client
	// Session holds the login and renews it.
	Session *session.Manager
	Getenv  func(string) string
	// Now is the clock, and Zone the time zone that times are shown in.
	Now  func() time.Time
	Zone *time.Location
}

// New returns artifikctl as it runs for real.
func New(build ctlkit.Build, skill fs.FS) *ctlkit.Program {
	app := &App{
		Client:  artifik.NewClient(),
		Session: &session.Manager{Store: keystore.Keyring(Name, "token"), LockPath: session.LockFile(Name)},
		Getenv:  os.Getenv,
		Now:     time.Now,
		Zone:    time.Local,
	}
	return app.program(build, skill)
}

func (a *App) program(build ctlkit.Build, skill fs.FS) *ctlkit.Program {
	p := ctlkit.New(Name, summary, build)
	p.URL = "https://github.com/langtind/" + Name
	p.Skill = skill
	p.Commands = a.commands()
	p.Hint = hint
	a.Client.UserAgent = p.UserAgent()
	a.Session.Renew = a.Client.RenewSession
	a.Session.Now = a.Now
	a.Client.Now = a.Now
	return p
}

// hint says what to do about an error that cannot say it itself.
func hint(err error) string {
	if ctlkit.KindOf(err) == ctlkit.KindAuth {
		return fmt.Sprintf(`log in again: run "%s help login" for the steps.`, Name)
	}
	return ""
}

// loginError adds what to do to an error from the session.
func loginError(err error) error {
	switch {
	case errors.Is(err, session.ErrNotLoggedIn):
		return ctlkit.Authf("%w", err).WithHint(`only "mine" needs a login. Run "%s help login" for the steps, or set %s.`, Name, envToken)
	case errors.Is(err, session.ErrExpired):
		return ctlkit.Authf("%w", err).WithHint(`log in again: run "%s help login" for the steps.`, Name)
	}
	return err
}

// authorize gives the client a token: the one from the environment, or else
// the stored login, renewed when it is about to run out. A stored login is
// also renewed when Artifik refuses its token in the middle of a run.
func (a *App) authorize(ctx context.Context) error {
	if token := strings.TrimSpace(a.Getenv(envToken)); token != "" {
		a.Client.Token = token
		return nil
	}
	token, err := a.Session.Token(ctx)
	if err != nil {
		return loginError(err)
	}
	a.Client.Token = token
	a.Client.Renew = func(ctx context.Context) (string, error) {
		token, err := a.Session.Refresh(ctx, a.Client.Token)
		return token, loginError(err)
	}
	return nil
}
