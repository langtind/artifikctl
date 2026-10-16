package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/httpx"
	"github.com/langtind/ctlkit/session"

	"github.com/langtind/artifikctl/internal/artifik"
)

const defaultLimit = 20

// types are the procurement types the web client's filter offers, in lower
// case as ctlkit matches them. Artifik takes them in upper case.
var types = []string{"competition", "rfi", "information", "information_indicative", "dps", "qualification_announcement", "notice_of_intent", "planning"}

// procurementArg is not checked as an id by ctlkit, since it also takes the
// address of the procurement's page. procurementID reads only the number.
var procurementArg = ctlkit.Arg{Name: "procurement-id", Required: true, Help: "the id as procurements prints it, or the address of its page in Artifik"}

// commands is the command table, the single description of the command
// line. Add a command or a flag here and nowhere else.
func (a *App) commands() []ctlkit.Command {
	return []ctlkit.Command{
		{
			Name:    "login",
			Summary: "Take over a login made in a browser, for the commands that need one",
			Long:    loginSteps,
			Flags: []ctlkit.Flag{
				{Name: "state-stdin", Kind: ctlkit.Bool, Help: "read a browser's storage state from stdin"},
			},
			Run: a.login,
		},
		{
			Name:    "logout",
			Summary: "Remove the stored login",
			Run:     a.logout,
		},
		{
			Name:    "status",
			Summary: "Show whether a login is stored and how long it lasts, without calling Artifik",
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag},
			Run:     a.status,
		},
		{
			Name:    "refresh",
			Summary: "Renew the stored login now",
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag},
			Run:     a.refresh,
		},
		{
			Name:    "procurements",
			Summary: "Search the published procurements",
			Long: `Lists the procurements every buyer on Artifik has published, with no login. Artifik lists the open ones mostly first, but not in deadline order, and the order can change between two runs.

--open keeps only those a supplier can still take part in: the next deadline is for bids or for requests to take part, and it has not passed. Artifik cannot filter on that, so the tool reads page after page of 100 until it has --limit of them.`,
			Args: []ctlkit.Arg{{Name: "query", Help: "words to search for in names and descriptions"}},
			Flags: []ctlkit.Flag{
				{Name: "type", Kind: ctlkit.List, Value: "type", Enum: types, Help: "only these procurement types"},
				{Name: "buyer", Kind: ctlkit.List, Value: "buyer-id", Help: "only these buyers, by the buyer id procurements prints"},
				{Name: "open", Kind: ctlkit.Bool, Help: "only those whose deadline for bids or for taking part has not passed"},
				{Name: "page", Kind: ctlkit.Int, Value: "n", Default: "1", Help: "the page of 100 to start from"},
				ctlkit.LimitFlag(defaultLimit, artifik.PageSize), ctlkit.JSONFlag, ctlkit.FieldsFlag,
			},
			Run: a.procurements,
		},
		{
			Name:    "procurement",
			Summary: "Show one procurement: deadlines, lots, CPV codes and attachments",
			Args:    []ctlkit.Arg{procurementArg},
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.procurement,
		},
		{
			Name:    "questions",
			Summary: "Show the public questions and answers of a procurement",
			Args:    []ctlkit.Arg{procurementArg},
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.questions,
		},
		{
			Name:    "download",
			Summary: "Save an attachment of a procurement, or all of them",
			Long:    `Give the attachment by its number or its name, as procurement lists them. --all saves every attachment into --dir, and stops before it fetches anything if one of the files is there already. A file that exists is never replaced.`,
			Args:    []ctlkit.Arg{procurementArg, {Name: "attachment", Help: "the number or the name of the attachment"}},
			Flags: []ctlkit.Flag{
				{Name: "all", Kind: ctlkit.Bool, Help: "save every attachment"},
				{Name: "dir", Kind: ctlkit.String, Value: "path", Default: ".", Help: "where --all saves the files"},
				ctlkit.OutputFlag, ctlkit.JSONFlag,
			},
			Run: a.download,
		},
		{
			Name:    "mine",
			Summary: "List the procurements you have bid on or been invited to, newest first",
			Flags:   []ctlkit.Flag{ctlkit.LimitFlag(defaultLimit, artifik.MaxMine), ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.mine,
		},
	}
}

// loginSteps is the long help of login.
const loginSteps = `Only "mine" needs a login. Searching, reading and downloading work without one.

Artifik logs in with an SMS code or a single sign-on, so the login is made in a browser: open it with "playwright-cli open https://app.artifik.no/login --headed", log in, run "playwright-cli state-save state.json", and then "` + Name + ` login --state-stdin < state.json". Delete state.json afterwards.

The login is kept in the system keyring and renews itself. It lasts 7 days after the last time ` + Name + ` used it.`

func (a *App) login(ctx context.Context, c *ctlkit.Call) error {
	if !c.Bool("state-stdin") {
		return ctlkit.Usagef("give the login with --state-stdin").WithHint(`run "%s help login" for the steps.`, Name)
	}
	state, err := c.ReadAll()
	if err != nil {
		return fmt.Errorf("read the browser state: %w", err)
	}
	cookie, err := artifik.SessionFromBrowser(state)
	if err != nil {
		return err
	}
	// Renewing at once checks that Artifik takes the cookie, and tells how
	// long the login lasts, which the browser state does not.
	login, err := a.Client.RenewSession(ctx, cookie)
	if err != nil {
		if ctlkit.KindOf(err) == ctlkit.KindAuth {
			return ctlkit.Authf("Artifik did not take the login: %w", err).WithHint("log in again in the browser, and save the state while the page is open.")
		}
		return err
	}
	return a.store(c, login)
}

func (a *App) store(c *ctlkit.Call, login session.Session) error {
	if err := a.Session.Save(login); err != nil {
		return fmt.Errorf("store the login in the keyring: %w", err)
	}
	if a.Getenv(envToken) != "" {
		c.Note("%s is set and takes precedence over the stored login.", envToken)
	}
	fmt.Fprintln(c.Stdout, "Logged in. The login is in the system keyring.")
	if !login.RefreshExpires.IsZero() {
		fmt.Fprintf(c.Stdout, "It renews itself, and lasts until %s if %s is not used before then.\n", login.RefreshExpires.In(a.Zone).Format("2006-01-02"), Name)
	}
	return nil
}

func (a *App) logout(ctx context.Context, c *ctlkit.Call) error {
	if err := a.Session.Clear(); err != nil {
		return fmt.Errorf("remove the login from the keyring: %w", err)
	}
	if a.Getenv(envToken) != "" {
		c.Note("%s is still set and keeps working.", envToken)
	}
	fmt.Fprintln(c.Stdout, "Removed the stored login.")
	return nil
}

// loginStatus is what status and refresh print.
type loginStatus struct {
	LoggedIn bool `json:"loggedIn"`
	// Source is "keyring", or "environment" when the variable is set.
	Source string `json:"source,omitempty"`
	// AccessExpires is when the access token runs out. It is renewed
	// before then, as long as RenewableUntil has not passed.
	AccessExpires string `json:"accessExpires,omitempty"`
	// RenewableUntil is when the user has to log in again unless the login
	// is renewed first. Every renewal moves it.
	RenewableUntil string `json:"renewableUntil,omitempty"`
}

func (a *App) loginStatus() (loginStatus, error) {
	if strings.TrimSpace(a.Getenv(envToken)) != "" {
		return loginStatus{LoggedIn: true, Source: "environment"}, nil
	}
	login, err := a.Session.Load()
	if errors.Is(err, session.ErrNotLoggedIn) {
		return loginStatus{}, nil
	}
	if err != nil {
		return loginStatus{}, err
	}
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	status := loginStatus{Source: "keyring", AccessExpires: stamp(login.AccessExpires), RenewableUntil: stamp(login.RefreshExpires)}
	now := a.Now()
	status.LoggedIn = login.AccessExpires.IsZero() || now.Before(login.AccessExpires) ||
		(login.RefreshToken != "" && (login.RefreshExpires.IsZero() || now.Before(login.RefreshExpires)))
	return status, nil
}

func (a *App) status(ctx context.Context, c *ctlkit.Call) error {
	status, err := a.loginStatus()
	if err != nil {
		return err
	}
	return c.Print(status, func(w io.Writer) { a.renderStatus(w, status) })
}

func (a *App) refresh(ctx context.Context, c *ctlkit.Call) error {
	if strings.TrimSpace(a.Getenv(envToken)) != "" {
		return ctlkit.Usagef("%s is set, and a cookie from the environment cannot be renewed", envToken)
	}
	login, err := a.Session.Load()
	if err != nil {
		return loginError(err)
	}
	if _, err := a.Session.Refresh(ctx, login.AccessToken); err != nil {
		return loginError(err)
	}
	return a.status(ctx, c)
}

// procurementList is what procurements prints. Total is how many Artifik
// has for the search, before --open. Truncated says there are more than
// were shown.
type procurementList struct {
	Items     []artifik.Summary `json:"items"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
	// NextPage is the --page to go on from, when the list was cut off at
	// the end of a page.
	NextPage int `json:"nextPage,omitempty"`
}

func (a *App) procurements(ctx context.Context, c *ctlkit.Call) error {
	limit := c.Int("limit")
	if limit < 1 || limit > artifik.PageSize {
		return ctlkit.Usagef("--limit takes 1 to %d, not %d", artifik.PageSize, limit)
	}
	query := artifik.Query{Search: strings.TrimSpace(c.Arg(0)), BuyerIDs: c.List("buyer"), Page: c.Int("page")}
	for _, t := range c.List("type") {
		query.Types = append(query.Types, strings.ToUpper(t))
	}
	if query.Page < 1 {
		return ctlkit.Usagef("--page counts from 1, not %d", query.Page)
	}
	open := c.Bool("open")
	now := a.Now()

	list := procurementList{Items: []artifik.Summary{}}
	for {
		page, err := a.Client.Procurements(ctx, query)
		if err != nil {
			return err
		}
		list.Total = page.Total
		more := query.Page < page.LastPage && len(page.Items) > 0
		for _, item := range page.Items {
			if open && !item.Open(now) {
				continue
			}
			if len(list.Items) == limit {
				list.Truncated = true
				break
			}
			list.Items = append(list.Items, item)
		}
		if list.Truncated || !more {
			break
		}
		query.Page++
		if len(list.Items) == limit {
			// The limit was reached at the end of a page. More may follow,
			// and --page goes on from here.
			list.Truncated, list.NextPage = true, query.Page
			break
		}
	}
	return c.Print(list, func(w io.Writer) { a.renderProcurements(w, list, open) })
}

func (a *App) procurement(ctx context.Context, c *ctlkit.Call) error {
	id, err := procurementID(c.Arg(0))
	if err != nil {
		return err
	}
	p, err := a.Client.Procurement(ctx, id)
	if err != nil {
		return unknownID(err, id)
	}
	result := procurementPage{Procurement: p, Questions: -1}
	// The questions are a page of their own at Artifik. Without them the
	// procurement is still worth showing, so a failure is only a note.
	if questions, err := a.Client.Questions(ctx, id); err == nil {
		result.Questions = len(questions)
	} else {
		c.Note("Could not count the questions: %v", err)
	}
	return c.Print(result, func(w io.Writer) { a.renderProcurement(w, result) })
}

// procurementPage is what procurement prints. Questions is how many public
// questions it has, or -1 when they could not be read.
type procurementPage struct {
	*artifik.Procurement
	Questions int `json:"questions"`
}

type questionList struct {
	Items []artifik.Question `json:"items"`
}

func (a *App) questions(ctx context.Context, c *ctlkit.Call) error {
	id, err := procurementID(c.Arg(0))
	if err != nil {
		return err
	}
	questions, err := a.Client.Questions(ctx, id)
	if err != nil {
		return unknownID(err, id)
	}
	list := questionList{Items: questions}
	return c.Print(list, func(w io.Writer) { a.renderQuestions(w, id, list) })
}

// savedFiles is what download --all prints.
type savedFiles struct {
	Saved []ctlkit.SavedFile `json:"saved"`
}

func (a *App) download(ctx context.Context, c *ctlkit.Call) error {
	id, err := procurementID(c.Arg(0))
	if err != nil {
		return err
	}
	all, which := c.Bool("all"), strings.TrimSpace(c.Arg(1))
	switch {
	case all && which != "":
		return ctlkit.Usagef("give an attachment or --all, not both")
	case !all && which == "":
		return ctlkit.Usagef("give the attachment by its number or its name, or --all").WithHint(`run "%s procurement %d" to see the attachments.`, Name, id)
	case all && c.Given("output"):
		return ctlkit.Usagef("--all saves into --dir, and takes no --output")
	case !all && c.Given("dir"):
		return ctlkit.Usagef("--dir is for --all. Give one file a path with --output")
	}

	p, err := a.Client.Procurement(ctx, id)
	if err != nil {
		return unknownID(err, id)
	}
	if len(p.Files) == 0 {
		return ctlkit.NotFoundf("procurement %d has no attachments", id)
	}
	if !all {
		file, err := pickFile(p, which)
		if err != nil {
			return err
		}
		data, err := a.Client.Download(ctx, id, file.Name)
		if err != nil {
			return err
		}
		return c.Save(data, file.Name)
	}
	return a.downloadAll(ctx, c, p)
}

// downloadAll saves every attachment into --dir. It looks at every path
// before it fetches the first file, so that a file that is there already
// stops the run before anything is written.
func (a *App) downloadAll(ctx context.Context, c *ctlkit.Call, p *artifik.Procurement) error {
	dir := c.String("dir")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ctlkit.Usagef("--dir %s is not a directory", dir)
	}
	paths := make([]string, len(p.Files))
	seen := map[string]bool{}
	for i, file := range p.Files {
		path := filepath.Join(dir, ctlkit.FileName(file.Name, nil))
		if seen[path] {
			return ctlkit.Errorf(ctlkit.KindConflict, "two attachments would both be saved as %s", path).WithHint("save them one at a time with --output.")
		}
		seen[path] = true
		if _, err := os.Stat(path); err == nil {
			return ctlkit.Errorf(ctlkit.KindConflict, "%s exists already", path).WithHint("give another --dir.")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		paths[i] = path
	}

	result := savedFiles{Saved: []ctlkit.SavedFile{}}
	for i, file := range p.Files {
		data, err := a.Client.Download(ctx, p.ID, file.Name)
		if err != nil {
			return fmt.Errorf("%s (saved %d of %d before it): %w", file.Name, len(result.Saved), len(p.Files), err)
		}
		if err := writeNew(paths[i], data); err != nil {
			return err
		}
		result.Saved = append(result.Saved, ctlkit.SavedFile{Path: paths[i], Bytes: len(data), ContentType: ctlkit.ContentType(paths[i], data)})
	}
	return c.Print(result, func(w io.Writer) {
		for _, saved := range result.Saved {
			fmt.Fprintf(w, "Saved %s (%d bytes, %s).\n", saved.Path, saved.Bytes, saved.ContentType)
		}
	})
}

// writeNew writes a file that must not exist.
func writeNew(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return ctlkit.Errorf(ctlkit.KindConflict, "%s exists already", path).WithHint("give another --dir.")
	}
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// pickFile finds an attachment by its number, counting from 1 as
// procurement prints them, or by its name.
func pickFile(p *artifik.Procurement, which string) (artifik.File, error) {
	if n, err := strconv.Atoi(which); err == nil {
		if n >= 1 && n <= len(p.Files) {
			return p.Files[n-1], nil
		}
		return artifik.File{}, ctlkit.NotFoundf("procurement %d has attachments 1 to %d, not %d", p.ID, len(p.Files), n).
			WithHint(`run "%s procurement %d" to see them.`, Name, p.ID)
	}
	for _, file := range p.Files {
		if file.Name == which {
			return file, nil
		}
	}
	return artifik.File{}, ctlkit.NotFoundf("procurement %d has no attachment named %q", p.ID, which).
		WithHint(`run "%s procurement %d" to see them, and give the name exactly or the number.`, Name, p.ID)
}

// mineList is what mine prints.
type mineList struct {
	Items     []artifik.Mine `json:"items"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`
}

func (a *App) mine(ctx context.Context, c *ctlkit.Call) error {
	limit := c.Int("limit")
	if limit < 1 || limit > artifik.MaxMine {
		return ctlkit.Usagef("--limit takes 1 to %d, not %d", artifik.MaxMine, limit)
	}
	if err := a.authorize(ctx); err != nil {
		return err
	}
	page, err := a.Client.Mine(ctx, limit)
	if err != nil {
		return loginError(err)
	}
	list := mineList{Items: page.Items, Total: page.Total, Truncated: page.Total > len(page.Items)}
	return c.Print(list, func(w io.Writer) { a.renderMine(w, list) })
}

// pageAddress is the path of a procurement's page, as a link from Doffin
// or from the web client has it.
var pageAddress = regexp.MustCompile(`^/procurements/([0-9]+)(/.*)?$`)

func procurementID(arg string) (int, error) {
	arg = strings.TrimSpace(arg)
	if u, err := url.Parse(arg); err == nil && u.Host == artifik.Host && (u.Scheme == "https" || u.Scheme == "http") {
		if m := pageAddress.FindStringSubmatch(u.Path); m != nil {
			arg = m[1]
		}
	}
	id, err := strconv.Atoi(arg)
	if err != nil || id < 1 {
		return 0, ctlkit.Usagef("a procurement id is a number, not %q", arg).WithHint(`run "%s procurements" to see the ids.`, Name)
	}
	return id, nil
}

// unknownID turns the 500 Artifik answers for an id it does not know into
// not found.
func unknownID(err error, id int) error {
	if httpx.Status(err) != http.StatusInternalServerError && ctlkit.KindOf(err) != ctlkit.KindNotFound {
		return err
	}
	return ctlkit.NotFoundf("Artifik has no public procurement %d: %w", id, err).
		WithHint(`Artifik answers 500 for an id it does not know, so the id may be wrong, or Artifik failing. Run "%s procurements" to see the ids.`, Name)
}
