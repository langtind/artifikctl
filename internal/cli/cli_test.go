package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/ctltest"
	"github.com/langtind/ctlkit/keystore"
	"github.com/langtind/ctlkit/session"

	"github.com/langtind/artifikctl/internal/artifik"
)

// harness runs artifikctl against a fake Artifik, with the login in memory.
type harness struct {
	t       *testing.T
	fake    *ctltest.Fake
	tokens  *keystore.Memory
	env     map[string]string
	lockDir string
}

var (
	testZone = time.FixedZone("CEST", 2*60*60)
	testTime = time.Date(2026, 10, 16, 12, 0, 0, 0, testZone)
)

const (
	listRoute   = "routes/procurements"
	detailRoute = "routes/procurements_.$proc_id"
	qaRoute     = "routes/procurements_.$proc_id.qas"
	mineRoute   = "routes/_index"
)

// turbo writes v as the turbo-stream a React Router loader answers with,
// under one route, the way reference/README.md describes it.
func turbo(route string, v any) string {
	var flat []any
	var add func(v any) int
	add = func(v any) int {
		i := len(flat)
		flat = append(flat, nil)
		switch v := v.(type) {
		case map[string]any:
			obj := map[string]int{}
			for k, x := range v {
				key := add(k)
				obj[fmt.Sprintf("_%d", key)] = add(x)
			}
			flat[i] = obj
		case []any:
			list := []int{}
			for _, x := range v {
				list = append(list, add(x))
			}
			flat[i] = list
		case nil:
			// A null in an object is the constant -5. As an element of
			// its own it is written as null.
			flat[i] = nil
		default:
			flat[i] = v
		}
		return i
	}
	add(map[string]any{route: map[string]any{"data": v}})
	data, _ := json.Marshal(flat)
	return string(data) + "\n"
}

// asAny turns a JSON text into the any that turbo takes.
func asAny(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// cookie is a login cookie as Artifik writes it: base64 JSON with the two
// tokens, a dot and a signature. The access token is a JWT that runs out at
// exp.
func cookie(name string, exp time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix())))
	inside, _ := json.Marshal(map[string]string{"accessToken": "e30." + payload + ".sig", "refreshToken": "refresh-" + name})
	return base64.RawURLEncoding.EncodeToString(inside) + "." + name
}

const list = `{"procurements":[
	{"id":101,"name":"Snow clearing","type":"COMPETITION","phase":"Preparing bids","description":"<p>Clearing &amp; salting</p><ul><li><p>roads</p></li></ul>",
	 "organization":{"id":"org-a","name":"Made-up Municipality"},"currentDeadline":{"date":"2026-11-01T11:00:00.000Z","type":"submission"},"budget":1200000,"place":"Nowhere","lots":[]},
	{"id":102,"name":"Old cleaning","type":"COMPETITION","phase":"Completed","description":"",
	 "organization":{"id":"org-b","name":"Made-up County"},"currentDeadline":{"date":"2027-01-01T11:00:00.000Z","type":"acceptance"},"budget":null,"place":"","lots":[]},
	{"id":103,"name":"Paper","type":"RFI","phase":"Accepting Messages","description":"",
	 "organization":{"id":"org-b","name":"Made-up County"},"currentDeadline":{"date":"2026-10-30T11:00:00.000Z","type":"submission"},"budget":null,"place":"","lots":[{"id":1,"name":"A"}]}
	],"total":3,"pagination":{"lastPage":1,"nextPage":2,"prevPage":0}}`

const detail = `{"procurement":{"id":101,"name":"Snow clearing","type":"COMPETITION","phase":"Preparing bids","procedure":"Open",
	"description":"<p>Clearing</p>","organization":{"id":"org-a","name":"Made-up Municipality"},
	"currentDeadline":{"date":"2026-11-01T11:00:00.000Z","type":"submission"},"total_value":2400000,"currency":"NOK",
	"cpv_codes":{"main_cpv":"90620000","aditional_cpv":["90630000"]},"performance_places":["NO081"],"externalReferenceUrl":null,
	"estimatedContractStartDate":"2027-01-31T23:00:00.000Z","reduce_participants_by_selection_criteria":"__YES__","suppliersLimitReason":"See 5.2",
	"procurementInformationSmartDoc":{"id":3,"version":1,"docJSON":{"type":"doc","content":[
		{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"6 Evaluation"}]},
		{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"6.3 Weighting"}]},
		{"type":"paragraph","content":[{"type":"text","text":"Quality counts "},{"type":"text","marks":[{"type":"bold"}],"text":"most"},{"type":"text","text":"."}]},
		{"type":"request","attrs":{"type":"files","config":{"required":false}},"content":[{"type":"text","text":"Upload references"}]}]}},
	"timeline":[{"name":"SUBMISSION","date":"2026-11-01T11:00:00.000Z","type":"deadline"}],
	"lots":[{"id":7,"name":"Lot 1: North","description":"North side","duration_months":24,"estimated_value":1200000,
		"selectionCriteria":[{"title":"Company registration","eFormsType":"slc-suit-reg-trade","is_documentation_required":true,"description":"",
			"smartDocRequest":{"id":1,"version":1,"docJSON":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"A certificate"},{"type":"hardBreak"},{"type":"text","text":"not older than 6 months"}]}]}}}],
		"awardCriteria":[{"title":"Price","awardCriteriaType":"price","percentage":40,"description":null},{"title":"Quality","awardCriteriaType":"quality","percentage":60,"description":null}],
		"requirementsSmartDoc":{"id":2,"version":1,"docJSON":{"type":"doc","content":[
			{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"Documents"}]},
			{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"a plan"}]}]}]},
			{"type":"request","attrs":{"type":"files","config":{"required":true}},"content":[{"type":"text","text":"Upload the plan"}]}]}}}],
	"files":[
		{"name":"b.xlsx","lastModified":"2026-10-14T10:00:00.000Z","orderIndex":1,"isDeleted":false,"key":"procurementAttachments/101/b"},
		{"name":"gone.pdf","lastModified":"2026-10-14T10:00:00.000Z","orderIndex":2,"isDeleted":true,"key":"procurementAttachments/101/g"},
		{"name":"a.pdf","lastModified":"2026-10-14T10:00:00.000Z","orderIndex":0,"isDeleted":false,"key":"procurementAttachments/101/a"}]},
	"tenders":null}`

// newHarness returns a fake Artifik that serves the list above, one
// procurement with two attachments, and a renewal that hands out a new
// cookie every time.
func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, fake: ctltest.NewFake(t), tokens: &keystore.Memory{}, env: map[string]string{}, lockDir: t.TempDir()}
	h.fake.Check = func(t *testing.T, c ctltest.Call) {
		if got := c.Header.Get("User-Agent"); got != Name+"/1.2.3 (+https://github.com/langtind/"+Name+")" {
			t.Errorf("User-Agent = %q", got)
		}
	}
	h.fake.On("GET /procurements.data", http.StatusOK, turbo(listRoute, asAny(t, list)))
	h.fake.On("GET /procurements/101.data", http.StatusOK, turbo(detailRoute, asAny(t, detail)))
	h.fake.On("GET /procurements/999.data", http.StatusInternalServerError, ``)
	h.fake.On("GET /procurements/101/qas.data", http.StatusOK, turbo(qaRoute, asAny(t,
		`{"qas":[{"id":1,"title":"Salt","question":"Which salt?","answer":"<p>Any.</p>","publishedDate":"2026-10-15T08:00:00.000Z","files":[]}],"proc_id":"101"}`)))
	h.fake.Handle("POST /api/manage-files", func(c ctltest.Call) ctltest.Answer {
		form, _ := url.ParseQuery(c.Body)
		if form.Get("endpoint") != "publicProcurement" || form.Get("intent") != "download" || form.Get("proc_id") != "101" {
			return ctltest.Answer{Status: http.StatusBadRequest}
		}
		return ctltest.Answer{Body: fmt.Sprintf(`{"url":"%s/files/%s?X-Amz-Expires=300"}`, h.fake.URL(), form.Get("file_name"))}
	})
	h.fake.On("GET /files/a.pdf", http.StatusOK, "%PDF-1.7 made up")
	h.fake.On("GET /files/b.xlsx", http.StatusOK, "PK\x03\x04 made up sheet")
	// A renewal hands out a new cookie every time, as Artifik does.
	renewals := 0
	h.fake.Handle("GET /api/ensure-session", func(c ctltest.Call) ctltest.Answer {
		if c.Cookies["artifik-session"] == "" {
			return ctltest.Answer{Status: http.StatusUnauthorized, Body: `{"error":"No refresh token"}`}
		}
		renewals++
		value := cookie(fmt.Sprintf("renewed-%d", renewals), testTime.Add(2*time.Hour))
		return ctltest.Answer{Body: `{"success":true}`, Header: http.Header{"Set-Cookie": {"artifik-session=" + value + "; Max-Age=604800; Path=/; HttpOnly; Secure"}}}
	})
	h.fake.Handle("GET /_root.data", func(c ctltest.Call) ctltest.Answer {
		if c.Cookies["artifik-session"] == "" || strings.HasSuffix(c.Cookies["artifik-session"], ".stale") {
			return ctltest.Answer{Status: http.StatusAccepted, Body: `[["SingleFetchRedirect",1],{"_2":3,"_4":5},"redirect","/login?redirectTo=/","status",302]` + "\n"}
		}
		return ctltest.Answer{Body: turbo(mineRoute, asAny(t, `{"allProcurements":[
			{"id":201,"name":"My bid","type":"COMPETITION","phase":"End of competition","organization":{"id":"org-a","name":"Made-up Municipality"},
			 "currentDeadline":{"date":"2026-12-01T11:00:00.000Z","type":"contract signing"},"status":"PUBLISHED","tenderId":77,"supplierInfo":{"supplierStatus":"SUBMITTED_BID"}}],
			"total":2,"pagination":{"currentPage":1,"totalPages":2,"pageSize":1}}`))}
	})
	return h
}

// login stores a login. left is how long the access token has to live.
func (h *harness) login(name string, left time.Duration) {
	h.t.Helper()
	value := cookie(name, testTime.Add(left))
	m := session.Manager{Store: h.tokens}
	err := m.Save(session.Session{AccessToken: value, AccessExpires: testTime.Add(left), RefreshToken: value, RefreshExpires: testTime.Add(7 * 24 * time.Hour)})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) stored() session.Session {
	h.t.Helper()
	m := session.Manager{Store: h.tokens}
	s, _ := m.Load()
	return s
}

// program returns artifikctl wired to the fake service.
func (h *harness) program() *ctlkit.Program {
	base, _ := url.Parse(h.fake.URL())
	app := &App{
		Client:  &artifik.Client{HTTP: h.fake.Client(), BaseURL: h.fake.URL(), FileStore: url.URL{Scheme: base.Scheme, Host: base.Host}},
		Session: &session.Manager{Store: h.tokens, LockPath: filepath.Join(h.lockDir, "renew.lock")},
		Getenv:  func(name string) string { return h.env[name] },
		Now:     func() time.Time { return testTime },
		Zone:    testZone,
	}
	return app.program(ctlkit.Build{Version: "1.2.3"}, os.DirFS("../../skills/"+Name))
}

// run runs one command. h.fake.Calls holds what it sent.
func (h *harness) run(stdin string, args ...string) (code int, stdout, stderr string) {
	h.t.Helper()
	h.fake.Reset()
	return ctltest.Run(h.program(), stdin, args...)
}

func TestContract(t *testing.T) {
	ctltest.Contract(t, newHarness(t).program, "../../skills/"+Name+"/SKILL.md", "../../README.md")
}

// artifikctl changes nothing at Artifik, so ctltest.ChangeNeedsAYes has no
// command to check.

func TestRefreshRenewsEveryTime(t *testing.T) {
	h := newHarness(t)
	h.login("stored", time.Hour)
	ctltest.RefreshRenewsEveryTime(t, h.program, h.stored)
}

func TestProcurementsAsMarkdown(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("", "procurements", "snow", "--limit", "1")
	want := `# Procurements

- Snow clearing
  id: 101
  Buyer: Made-up Municipality
  Type: COMPETITION
  Phase: Preparing bids
  Next deadline: 2026-11-01T13:00:00+02:00 (submission)
  Budget: 1 200 000
  Place: Nowhere
  About: Clearing & salting - roads

Showing 1. Artifik has 3 for this search.
There are more. Raise --limit to see them.
`
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("exit = %d, stderr = %q, stdout:\n%s", code, stderr, stdout)
	}
	if h.fake.Sent() != "GET /procurements.data" || h.fake.Calls[0].Query != "_routes=routes%2Fprocurements&search=snow" || len(h.fake.Calls[0].Cookies) != 0 {
		t.Errorf("calls = %+v", h.fake.Calls)
	}
}

func TestProcurementsFilters(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "procurements", "--type", "competition,rfi", "--buyer", "org-b", "--page", "2", "--json")
	if q, _ := url.ParseQuery(h.fake.Calls[0].Query); code != 0 || q.Get("procurementTypes") != "COMPETITION,RFI" || q.Get("orgIds") != "org-b" || q.Get("page") != "2" {
		t.Errorf("exit = %d, query = %s", code, h.fake.Calls[0].Query)
	}
	var got procurementList
	ctltest.OneJSON(t, stdout, &got)
	if len(got.Items) != 3 || got.Truncated || got.Items[0].URL != "https://app.artifik.no/procurements/101" {
		t.Errorf("list = %+v", got)
	}

	if code, _, stderr := h.run("", "procurements", "--type", "tender"); code != 2 || !strings.Contains(stderr, "tender") {
		t.Errorf("a wrong type: exit = %d, stderr = %q", code, stderr)
	}
}

func TestOpenKeepsOnlyWhatCanStillBeBidOn(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "procurements", "--open", "--json", "--fields", "id")
	var got struct {
		Items []map[string]int `json:"items"`
	}
	ctltest.OneJSON(t, stdout, &got)
	if code != 0 || len(got.Items) != 2 || got.Items[0]["id"] != 101 || got.Items[1]["id"] != 103 {
		t.Errorf("exit = %d, items = %+v", code, got.Items)
	}
}

func TestOpenReadsOnUntilTheLimitIsMet(t *testing.T) {
	h := newHarness(t)
	h.fake.Handle("GET /procurements.data", func(c ctltest.Call) ctltest.Answer {
		q, _ := url.ParseQuery(c.Query)
		var page string
		switch q.Get("page") {
		case "":
			page = `{"procurements":[{"id":1,"phase":"Completed","currentDeadline":{"date":"2027-01-01T00:00:00Z","type":"acceptance"},"organization":{}}],"total":3,"pagination":{"lastPage":3}}`
		case "2":
			page = `{"procurements":[{"id":2,"phase":"Preparing bids","currentDeadline":{"date":"2026-10-01T00:00:00Z","type":"submission"},"organization":{}}],"total":3,"pagination":{"lastPage":3}}`
		default:
			page = `{"procurements":[{"id":3,"phase":"Preparing bids","currentDeadline":{"date":"2026-11-01T00:00:00Z","type":"submission"},"organization":{}}],"total":3,"pagination":{"lastPage":3}}`
		}
		return ctltest.Answer{Body: turbo(listRoute, asAny(t, page))}
	})
	code, stdout, _ := h.run("", "procurements", "--open", "--limit", "1", "--json")
	var got procurementList
	ctltest.OneJSON(t, stdout, &got)
	// The limit was met at the end of the last page, so nothing is cut off.
	if code != 0 || h.fake.Sent() != "GET /procurements.data, GET /procurements.data, GET /procurements.data" || len(got.Items) != 1 || got.Items[0].ID != 3 || got.Truncated {
		t.Errorf("exit = %d, sent = %s, list = %+v", code, h.fake.Sent(), got)
	}
}

func TestAProcurementInFull(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("", "procurement", "101")
	for _, want := range []string{
		"# Snow clearing\n\nid: 101\nBuyer: Made-up Municipality\n",
		"Estimated value: 2 400 000 NOK\nMain CPV: 90620000\nOther CPV: 90630000\nPlaces: NO081\n",
		"How many are invited: See 5.2\nPage: https://app.artifik.no/procurements/101\n",
		"## About\n\n> Clearing\n",
		"## Timeline\n\n- SUBMISSION: 2026-11-01T13:00:00+02:00\n",
		"### Lot 1: North\n\nDuration: 24 months\nEstimated value: 1 200 000\n\n> North side\n",
		"#### Qualification requirements\n\n1. Company registration (slc-suit-reg-trade), documentation required\n   Documentation:\n\n   > A certificate not older than 6 months\n",
		"Contract start: 2027-02-01T01:00:00+02:00\nInvites: a selection of the qualified suppliers\nHow many are invited: See 5.2\n",
		"## Competition document\n\nThe rules, the evaluation model and what to hand in are in the competition document. Read it with \"artifikctl document 101\".\n",
		"#### Award criteria\n\n- Price: 40 %, price\n- Quality: 60 %, quality\n",
		"#### Specification\n\n> Documents\n> - a plan\n> [Supplier to give: upload files, required] Upload the plan\n",
		// In the buyer's order, and without the deleted one.
		"## Attachments\n\n1. a.pdf\n2. b.xlsx\n",
		"## Questions\n\n1 public questions and answers. Read them with \"artifikctl questions 101\".\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if code != 0 || stderr != "" || h.fake.Calls[0].Query != "_routes=routes%2Fprocurements_.%24proc_id" || h.fake.Sent() != "GET /procurements/101.data, GET /procurements/101/qas.data" {
		t.Errorf("exit = %d, stderr = %q, calls = %+v", code, stderr, h.fake.Calls)
	}
}

func TestTheAddressOfAPageIsAnID(t *testing.T) {
	h := newHarness(t)
	for _, arg := range []string{"https://app.artifik.no/procurements/101", "https://app.artifik.no/procurements/101/details"} {
		if code, _, stderr := h.run("", "procurement", arg, "--json"); code != 0 || h.fake.Calls[0].Path != "/procurements/101.data" {
			t.Errorf("%s: exit = %d, stderr = %q", arg, code, stderr)
		}
	}
	for _, arg := range []string{"https://elsewhere.invalid/procurements/101", "https://app.artifik.no/contracts/101", "../101"} {
		if code, _, _ := h.run("", "procurement", arg); code != 2 || len(h.fake.Calls) != 0 {
			t.Errorf("%s: exit = %d, calls = %+v", arg, code, h.fake.Calls)
		}
	}
}

func TestOneLotWithoutANameIsTheProcurementItself(t *testing.T) {
	h := newHarness(t)
	h.fake.On("GET /procurements/101.data", http.StatusOK, turbo(detailRoute, asAny(t, `{"procurement":{"id":101,"name":"Time sheets",
		"description":"<p>The same text</p>","organization":{"name":"Made-up Agency"},"currentDeadline":{},
		"lots":[{"id":7,"name":null,"description":"The same text","awardCriteria":[{"title":"Price","awardCriteriaType":"price","percentage":100}]}],"files":[]}}`)))
	h.fake.On("GET /procurements/101/qas.data", http.StatusInternalServerError, ``)
	code, stdout, stderr := h.run("", "procurement", "101")
	if code != 0 || strings.Contains(stdout, "## Lots") || strings.Contains(stdout, "###") || strings.Count(stdout, "The same text") != 1 ||
		!strings.Contains(stdout, "\n## Award criteria\n\n- Price: 100 %, price\n") || strings.Contains(stdout, "## Questions") ||
		!strings.Contains(stderr, "Could not count the questions") {
		t.Errorf("exit = %d, stderr = %q, stdout:\n%s", code, stderr, stdout)
	}
}

func TestAnUnknownProcurementIsNotFound(t *testing.T) {
	code, stdout, _ := newHarness(t).run("", "procurement", "999", "--json")
	var doc ctltest.ErrorDocument
	ctltest.OneJSON(t, stdout, &doc)
	if code != 3 || doc.Error.Kind != "not_found" || !strings.Contains(doc.Error.Hint, "500 for an id it does not know") {
		t.Errorf("exit = %d, error = %+v", code, doc.Error)
	}
	if code, _, _ := newHarness(t).run("", "procurement", "x1"); code != 2 {
		t.Errorf("an id that is not a number: exit = %d", code)
	}
}

func TestTheCompetitionDocument(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "document", "101")
	want := "# Competition document: Snow clearing\n\n> ## 6 Evaluation\n>\n> ### 6.3 Weighting\n>\n> Quality counts most.\n>\n> [Supplier to give: upload files] Upload references\n"
	if code != 0 || stdout != want {
		t.Errorf("exit = %d, stdout = %q", code, stdout)
	}
	_, stdout, _ = h.run("", "document", "101", "--json")
	var doc artifik.Document
	ctltest.OneJSON(t, stdout, &doc)
	if doc.ID != 101 || doc.Asks != 1 || !strings.HasPrefix(doc.Text, "## 6 Evaluation\n\n### 6.3") {
		t.Errorf("doc = %+v", doc)
	}
}

func TestQuestions(t *testing.T) {
	code, stdout, _ := newHarness(t).run("", "questions", "101")
	want := "# Questions about procurement 101\n\n## Salt\n\nPublished: 2026-10-15T10:00:00+02:00\n\nQuestion:\n\n> Which salt?\n\nAnswer:\n\n> Any.\n"
	if code != 0 || stdout != want {
		t.Errorf("exit = %d, stdout = %q", code, stdout)
	}
}

func TestDownloadOneByNumberOrName(t *testing.T) {
	t.Chdir(t.TempDir())
	h := newHarness(t)
	code, stdout, stderr := h.run("", "download", "101", "1")
	if code != 0 || stdout != "Saved a.pdf (16 bytes, application/pdf).\n" || h.fake.Sent() != "GET /procurements/101.data, POST /api/manage-files, GET /files/a.pdf" {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q, sent = %s", code, stdout, stderr, h.fake.Sent())
	}
	if form, _ := url.ParseQuery(h.fake.Calls[1].Body); form.Get("file_name") != "a.pdf" || len(h.fake.Calls[2].Cookies) != 0 {
		t.Errorf("body = %q, cookies to the file store = %v", h.fake.Calls[1].Body, h.fake.Calls[2].Cookies)
	}
	if code, _, stderr = h.run("", "download", "101", "1"); code != 5 || !strings.Contains(stderr, "a.pdf exists already") {
		t.Errorf("again: exit = %d, stderr = %q", code, stderr)
	}
	if code, stdout, _ = h.run("", "download", "101", "b.xlsx", "--output", "sheet.xlsx"); code != 0 || !strings.HasPrefix(stdout, "Saved sheet.xlsx") {
		t.Errorf("by name: exit = %d, stdout = %q", code, stdout)
	}
	for _, args := range [][]string{{"download", "101", "3"}, {"download", "101", "gone.pdf"}} {
		if code, _, _ := h.run("", args...); code != 3 {
			t.Errorf("%v: exit = %d", args, code)
		}
	}
	if code, _, _ := h.run("", "download", "101"); code != 2 {
		t.Errorf("neither a file nor --all: exit = %d", code)
	}

	// --dir takes one file too, and is made when it is not there.
	dir := filepath.Join(t.TempDir(), "new", "sub")
	code, stdout, stderr = h.run("", "download", "101", "2", "--dir", dir, "--json")
	var saved ctlkit.SavedFile
	ctltest.OneJSON(t, stdout, &saved)
	if code != 0 || saved.Path != filepath.Join(dir, "b.xlsx") {
		t.Errorf("--dir: exit = %d, stderr = %q, saved = %+v", code, stderr, saved)
	}
	if code, _, _ := h.run("", "download", "101", "2", "--dir", dir, "--output", "x.xlsx"); code != 2 {
		t.Errorf("--dir and --output: exit = %d", code)
	}
}

func TestDownloadAllNeverReplacesAFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs")
	h := newHarness(t)
	code, stdout, stderr := h.run("", "download", "101", "--all", "--dir", dir, "--json")
	var got savedFiles
	ctltest.OneJSON(t, stdout, &got)
	if code != 0 || len(got.Saved) != 2 || got.Saved[1].Path != filepath.Join(dir, "b.xlsx") || got.Saved[1].ContentType != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("exit = %d, stderr = %q, saved = %+v", code, stderr, got)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.pdf")); string(data) != "%PDF-1.7 made up" {
		t.Errorf("a.pdf = %q", data)
	}

	// With one file there, nothing is fetched.
	if err := os.Remove(filepath.Join(dir, "a.pdf")); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = h.run("", "download", "101", "--all", "--dir", dir)
	if code != 5 || !strings.Contains(stderr, "b.xlsx exists already") || h.fake.Sent() != "GET /procurements/101.data" {
		t.Errorf("exit = %d, stderr = %q, sent = %s", code, stderr, h.fake.Sent())
	}
}

func TestAFileAddressOutsideTheFileStoreIsRefused(t *testing.T) {
	t.Chdir(t.TempDir())
	h := newHarness(t)
	h.fake.On("POST /api/manage-files", http.StatusOK, `{"url":"https://elsewhere.invalid/a.pdf"}`)
	code, _, stderr := h.run("", "download", "101", "1")
	if code != 1 || !strings.Contains(stderr, "outside its file store") || h.fake.Sent() != "GET /procurements/101.data, POST /api/manage-files" {
		t.Errorf("exit = %d, stderr = %q, sent = %s", code, stderr, h.fake.Sent())
	}
}

func TestMineNeedsTheLogin(t *testing.T) {
	h := newHarness(t)
	code, _, stderr := h.run("", "mine")
	if code != 4 || !strings.Contains(stderr, "not logged in") || len(h.fake.Calls) != 0 {
		t.Errorf("exit = %d, stderr = %q, calls = %+v", code, stderr, h.fake.Calls)
	}

	h.login("stored", time.Hour)
	code, stdout, stderr := h.run("", "mine", "--limit", "1")
	want := "# My procurements\n\n- My bid\n  id: 201\n  Buyer: Made-up Municipality\n  Phase: End of competition\n  My status: SUBMITTED_BID\n  Next deadline: 2026-12-01T13:00:00+02:00 (contract signing)\n\nShowing 1 of 2. Raise --limit to see more.\n"
	if code != 0 || stdout != want {
		t.Errorf("exit = %d, stderr = %q, stdout = %q", code, stderr, stdout)
	}
	if q, _ := url.ParseQuery(h.fake.Calls[0].Query); h.fake.Calls[0].Cookies["artifik-session"] != h.stored().AccessToken || q.Get("pageSize") != "1" || q.Get("_routes") != mineRoute {
		t.Errorf("calls = %+v", h.fake.Calls)
	}
}

func TestATurnedAwayLoginIsRenewedAndTheRequestSentAgain(t *testing.T) {
	h := newHarness(t)
	h.login("stale", time.Hour)
	code, _, stderr := h.run("", "mine")
	if code != 0 || h.fake.Sent() != "GET /_root.data, GET /api/ensure-session, GET /_root.data" {
		t.Errorf("exit = %d, stderr = %q, sent = %s", code, stderr, h.fake.Sent())
	}
	if got := h.stored(); !strings.HasSuffix(got.AccessToken, ".renewed-1") || !got.RefreshExpires.Equal(testTime.Add(7*24*time.Hour)) {
		t.Errorf("stored = %+v", got)
	}
}

func TestAnAccessTokenAboutToRunOutIsRenewedFirst(t *testing.T) {
	h := newHarness(t)
	h.login("stored", 2*time.Minute)
	code, _, stderr := h.run("", "mine")
	if code != 0 || h.fake.Sent() != "GET /api/ensure-session, GET /_root.data" || !strings.HasSuffix(h.fake.Calls[1].Cookies["artifik-session"], ".renewed-1") {
		t.Errorf("exit = %d, stderr = %q, sent = %s", code, stderr, h.fake.Sent())
	}
}

func TestALoginThatCannotBeRenewedSaysToLogInAgain(t *testing.T) {
	h := newHarness(t)
	h.login("stored", -time.Hour)
	h.fake.On("GET /api/ensure-session", http.StatusUnauthorized, `{"error":"No refresh token"}`)
	code, _, stderr := h.run("", "mine")
	if code != 4 || !strings.Contains(stderr, "No refresh token") || !strings.Contains(stderr, "help login") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestLoginTakesOverABrowserLogin(t *testing.T) {
	h := newHarness(t)
	state := fmt.Sprintf(`{"cookies":[
		{"name":"artifik-session","value":%q,"domain":"app.artifik.no"},
		{"name":"artifik-session","value":"somebody-else","domain":"other.invalid"}]}`, cookie("browser", testTime.Add(time.Hour)))
	code, stdout, stderr := h.run(state, "login", "--state-stdin")
	got := h.stored()
	if code != 0 || stdout != "Logged in. The login is in the system keyring.\nIt renews itself, and lasts until 2026-10-23 if artifikctl is not used before then.\n" ||
		h.fake.Sent() != "GET /api/ensure-session" || !strings.HasSuffix(h.fake.Calls[0].Cookies["artifik-session"], ".browser") ||
		!strings.HasSuffix(got.RefreshToken, ".renewed-1") || !got.AccessExpires.Equal(testTime.Add(2*time.Hour).Truncate(time.Second)) {
		t.Errorf("exit = %d, stdout = %q, stderr = %q, sent = %s, stored = %+v", code, stdout, stderr, h.fake.Sent(), got)
	}

	h.tokens.Secret = ""
	for _, state := range []string{`{"cookies":[]}`, `{"cookies":[{"name":"artifik-session","value":"e30.sig","domain":"app.artifik.no"}]}`} {
		if code, _, stderr := h.run(state, "login", "--state-stdin"); code != 4 || h.tokens.Secret != "" || len(h.fake.Calls) != 0 {
			t.Errorf("%s: exit = %d, stderr = %q", state, code, stderr)
		}
	}
	if code, _, _ := h.run("", "login"); code != 2 {
		t.Errorf("without --state-stdin: exit = %d", code)
	}
}

func TestStatusAndLogout(t *testing.T) {
	h := newHarness(t)
	if _, stdout, _ := h.run("", "status"); !strings.HasPrefix(stdout, "Not logged in. Only \"mine\" needs a login") {
		t.Errorf("not logged in: %q", stdout)
	}
	h.login("stored", time.Hour)
	code, stdout, _ := h.run("", "status")
	if code != 0 || !strings.HasPrefix(stdout, "Logged in.\nAccess token until: 2026-10-16T13:00:00+02:00\nRenews itself until: 2026-10-23T12:00:00+02:00\n") || len(h.fake.Calls) != 0 {
		t.Errorf("status: exit = %d, stdout = %q", code, stdout)
	}
	if code, _, _ := h.run("", "logout"); code != 0 || h.tokens.Secret != "" {
		t.Errorf("logout: exit = %d", code)
	}
}
