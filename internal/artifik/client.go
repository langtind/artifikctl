// Package artifik is the client for Artifik, read through the loaders of its
// web client. reference/README.md has what was observed.
package artifik

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/httpx"
	"github.com/langtind/ctlkit/session"
)

const (
	service = "Artifik"

	// Host is where the web client and its loaders are.
	Host = "app.artifik.no"

	// PageSize is how many procurements the public list gives per page. It
	// cannot be changed.
	PageSize = 100

	// MaxMine is the most of the user's own procurements one request asks for.
	MaxMine = 100

	cookieName = "artifik-session"
	// cookieLifetime is the Max-Age Artifik gives the login cookie. Every
	// renewal sets it again.
	cookieLifetime = 7 * 24 * time.Hour
)

type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string
	// Token is the login cookie. It is sent only by the requests that need a
	// login.
	Token string
	// Renew returns a new cookie after the service refused Token. The request
	// is then sent once more. Nil means a refusal is final.
	Renew func(ctx context.Context) (string, error)
	// Now is the clock a renewal dates the new login by.
	Now func() time.Time
	// FileStore is where attachments may be fetched from: the scheme, and
	// the end of the host.
	FileStore url.URL
}

func NewClient() *Client {
	return &Client{
		HTTP: httpx.NewClient(), BaseURL: "https://" + Host, Now: time.Now,
		FileStore: url.URL{Scheme: "https", Host: ".s3.eu-central-1.amazonaws.com"},
	}
}

// URL is the page of a procurement in the web client.
func URL(id int) string {
	return fmt.Sprintf("https://%s/procurements/%d", Host, id)
}

// Organization is the buyer.
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type deadline struct {
	Date string `json:"date"`
	Type string `json:"type"`
}

type lotWire struct {
	ID                int      `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	DurationMonths    *float64 `json:"duration_months"`
	EstimatedValue    *float64 `json:"estimated_value"`
	SelectionCriteria []struct {
		Title                 string          `json:"title"`
		EFormsType            string          `json:"eFormsType"`
		DocumentationRequired bool            `json:"is_documentation_required"`
		Description           string          `json:"description"`
		SmartDocRequest       json.RawMessage `json:"smartDocRequest"`
	} `json:"selectionCriteria"`
	AwardCriteria []struct {
		Title       string   `json:"title"`
		Type        string   `json:"awardCriteriaType"`
		Percentage  *float64 `json:"percentage"`
		Description string   `json:"description"`
	} `json:"awardCriteria"`
	RequirementsSmartDoc json.RawMessage `json:"requirementsSmartDoc"`
}

// summaryWire is a procurement as the public list and the user's own list
// give it.
type summaryWire struct {
	ID              int          `json:"id"`
	Name            string       `json:"name"`
	Type            string       `json:"type"`
	Phase           string       `json:"phase"`
	Description     string       `json:"description"`
	Organization    Organization `json:"organization"`
	CurrentDeadline deadline     `json:"currentDeadline"`
	Budget          *float64     `json:"budget"`
	Place           string       `json:"place"`
	Lots            []lotWire    `json:"lots"`
	// Only in the user's own list.
	Status       string `json:"status"`
	TenderID     *int   `json:"tenderId"`
	SupplierInfo *struct {
		SupplierStatus string `json:"supplierStatus"`
	} `json:"supplierInfo"`
}

// Summary is one procurement in a list.
type Summary struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Phase        string  `json:"phase"`
	Buyer        string  `json:"buyer"`
	BuyerID      string  `json:"buyerId"`
	Deadline     string  `json:"deadline,omitempty"`
	DeadlineType string  `json:"deadlineType,omitempty"`
	Budget       float64 `json:"budget,omitempty"`
	Place        string  `json:"place,omitempty"`
	Lots         int     `json:"lots"`
	Description  string  `json:"description,omitempty"`
	URL          string  `json:"url"`
}

func (w summaryWire) summary() Summary {
	s := Summary{
		ID: w.ID, Name: w.Name, Type: w.Type, Phase: w.Phase,
		Buyer: w.Organization.Name, BuyerID: w.Organization.ID,
		Deadline: w.CurrentDeadline.Date, DeadlineType: w.CurrentDeadline.Type,
		Place: w.Place, Lots: len(w.Lots), Description: text(w.Description), URL: URL(w.ID),
	}
	if w.Budget != nil {
		s.Budget = *w.Budget
	}
	return s
}

// Open says whether a supplier can still take part: the next deadline is
// for bids or for requests to take part, and it has not passed.
func (s Summary) Open(now time.Time) bool {
	if s.Phase == "Cancelled" || (s.DeadlineType != "submission" && s.DeadlineType != "qualification") {
		return false
	}
	t, err := time.Parse(time.RFC3339, s.Deadline)
	return err == nil && t.After(now)
}

// Query is a search of the public list.
type Query struct {
	Search string
	// Types are procurement types such as COMPETITION. Artifik takes them
	// as one comma separated value.
	Types    []string
	BuyerIDs []string
	// Page counts from 1.
	Page int
}

// Page is one page of the public list.
type Page struct {
	Items    []Summary
	Total    int
	LastPage int
}

// Procurements reads one page of the public list. It needs no login.
func (c *Client) Procurements(ctx context.Context, q Query) (*Page, error) {
	query := url.Values{}
	if q.Search != "" {
		query.Set("search", q.Search)
	}
	if len(q.Types) > 0 {
		query.Set("procurementTypes", strings.Join(q.Types, ","))
	}
	if len(q.BuyerIDs) > 0 {
		query.Set("orgIds", strings.Join(q.BuyerIDs, ","))
	}
	if q.Page > 1 {
		query.Set("page", strconv.Itoa(q.Page))
	}
	var data struct {
		Procurements []summaryWire `json:"procurements"`
		Total        int           `json:"total"`
		Pagination   struct {
			LastPage int `json:"lastPage"`
		} `json:"pagination"`
	}
	if err := c.loader(ctx, "/procurements.data", "routes/procurements", query, false, &data); err != nil {
		return nil, err
	}
	page := &Page{Items: make([]Summary, 0, len(data.Procurements)), Total: data.Total, LastPage: data.Pagination.LastPage}
	for _, p := range data.Procurements {
		page.Items = append(page.Items, p.summary())
	}
	return page, nil
}

// Event is a date in the timeline of a procurement.
type Event struct {
	Name string `json:"name"`
	Date string `json:"date"`
}

type Lot struct {
	ID             int     `json:"id"`
	Name           string  `json:"name,omitempty"`
	DurationMonths float64 `json:"durationMonths,omitempty"`
	EstimatedValue float64 `json:"estimatedValue,omitempty"`
	// Description is left out when it is the same as the procurement's.
	Description   string      `json:"description,omitempty"`
	Qualification []Criterion `json:"qualification"`
	Award         []Award     `json:"award"`
	// Specification is the lot's requirements document as text, with a
	// line for each thing the supplier is asked to give.
	Specification string `json:"specification,omitempty"`
}

// Criterion is a qualification requirement a supplier has to meet.
type Criterion struct {
	Title string `json:"title"`
	// Type is the eForms code, such as slc-sec-inf.
	Type                  string `json:"type,omitempty"`
	DocumentationRequired bool   `json:"documentationRequired"`
	Description           string `json:"description,omitempty"`
	// Documentation is what the supplier has to hand in to show the
	// requirement is met.
	Documentation string `json:"documentation,omitempty"`
}

// Award is a criterion the bids are weighed by.
type Award struct {
	Title       string  `json:"title"`
	Type        string  `json:"type"`
	Percentage  float64 `json:"percentage"`
	Description string  `json:"description,omitempty"`
}

// File is an attachment of a procurement.
type File struct {
	Name     string `json:"name"`
	Modified string `json:"modified,omitempty"`
}

// Procurement is one procurement in full.
type Procurement struct {
	Summary
	Procedure     string   `json:"procedure,omitempty"`
	Regulation    string   `json:"regulation,omitempty"`
	Threshold     string   `json:"threshold,omitempty"`
	Category      string   `json:"category,omitempty"`
	TotalValue    float64  `json:"totalValue,omitempty"`
	Currency      string   `json:"currency,omitempty"`
	MainCPV       string   `json:"mainCpv,omitempty"`
	OtherCPV      []string `json:"otherCpv,omitempty"`
	Places        []string `json:"places,omitempty"`
	ExternalID    string   `json:"externalId,omitempty"`
	ExternalURL   string   `json:"externalUrl,omitempty"`
	ContractStart string   `json:"contractStart,omitempty"`
	// ReducesParticipants says the buyer picks among the qualified
	// suppliers when more qualify than it invites, and LimitReason says how.
	ReducesParticipants bool   `json:"reducesParticipants"`
	LimitReason         string `json:"limitReason,omitempty"`
	// LimitSection is the section of the competition document LimitReason
	// points to, when the document has it.
	LimitSection string `json:"limitSection,omitempty"`
	// HasDocument says the procurement has a competition document, which
	// Client.Document reads.
	HasDocument bool    `json:"hasDocument"`
	Timeline    []Event `json:"timeline"`
	LotList     []Lot   `json:"lots"`
	Files       []File  `json:"files"`
}

type procurementWire struct {
	summaryWire
	Procedure        string   `json:"procedure"`
	Regulation       string   `json:"regulation"`
	Threshold        string   `json:"threshold"`
	ContractCategory string   `json:"contractCategory"`
	TotalValue       *float64 `json:"total_value"`
	Currency         string   `json:"currency"`
	CPV              *struct {
		Main  string   `json:"main_cpv"`
		Other []string `json:"aditional_cpv"`
	} `json:"cpv_codes"`
	PerformancePlaces []string        `json:"performance_places"`
	ExternalID        string          `json:"externalId"`
	ExternalURL       *string         `json:"externalReferenceUrl"`
	ContractStart     *string         `json:"estimatedContractStartDate"`
	ReduceBySelection string          `json:"reduce_participants_by_selection_criteria"`
	LimitReason       *string         `json:"suppliersLimitReason"`
	InformationDoc    json.RawMessage `json:"procurementInformationSmartDoc"`
	Timeline          []struct {
		Name string `json:"name"`
		Date string `json:"date"`
	} `json:"timeline"`
	Files []struct {
		Name         string `json:"name"`
		LastModified string `json:"lastModified"`
		OrderIndex   int    `json:"orderIndex"`
		IsDeleted    bool   `json:"isDeleted"`
	} `json:"files"`
}

func (w procurementWire) procurement() *Procurement {
	p := &Procurement{
		Summary:   w.summary(),
		Procedure: w.Procedure, Regulation: w.Regulation, Threshold: w.Threshold,
		Category: w.ContractCategory, Currency: w.Currency, Places: w.PerformancePlaces,
		ExternalID: w.ExternalID, Timeline: []Event{}, LotList: []Lot{}, Files: []File{},
	}
	if w.TotalValue != nil {
		p.TotalValue = *w.TotalValue
	}
	if w.CPV != nil {
		p.MainCPV, p.OtherCPV = w.CPV.Main, w.CPV.Other
	}
	if w.ExternalURL != nil {
		p.ExternalURL = *w.ExternalURL
	}
	if w.ContractStart != nil {
		p.ContractStart = *w.ContractStart
	}
	if w.LimitReason != nil {
		p.LimitReason = text(*w.LimitReason)
	}
	p.ReducesParticipants = w.ReduceBySelection == "__YES__"
	document := docMarkdown(rawDoc(w.InformationDoc))
	p.HasDocument = document != ""
	if m := sectionNumber.FindString(p.LimitReason); m != "" && hasSection(document, m) {
		p.LimitSection = m
	}
	for _, e := range w.Timeline {
		p.Timeline = append(p.Timeline, Event(e))
	}
	for _, l := range w.Lots {
		lot := Lot{ID: l.ID, Name: strings.TrimSpace(l.Name), Description: text(l.Description),
			Qualification: []Criterion{}, Award: []Award{}, Specification: docText(rawDoc(l.RequirementsSmartDoc))}
		if lot.Description == p.Description {
			lot.Description = ""
		}
		for _, c := range l.SelectionCriteria {
			criterion := Criterion{Title: c.Title, Type: c.EFormsType, DocumentationRequired: c.DocumentationRequired,
				Description: text(c.Description), Documentation: docText(rawDoc(c.SmartDocRequest))}
			lot.Qualification = append(lot.Qualification, criterion)
		}
		for _, a := range l.AwardCriteria {
			award := Award{Title: a.Title, Type: a.Type, Description: text(a.Description)}
			if a.Percentage != nil {
				award.Percentage = *a.Percentage
			}
			lot.Award = append(lot.Award, award)
		}
		if l.DurationMonths != nil {
			lot.DurationMonths = *l.DurationMonths
		}
		if l.EstimatedValue != nil {
			lot.EstimatedValue = *l.EstimatedValue
		}
		p.LotList = append(p.LotList, lot)
	}
	// Files come in the order the buyer gave them, which is the order the
	// web client shows.
	files := w.Files
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].OrderIndex < files[j-1].OrderIndex; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
	for _, f := range files {
		if !f.IsDeleted {
			p.Files = append(p.Files, File{Name: f.Name, Modified: f.LastModified})
		}
	}
	return p
}

// Procurement reads one procurement. It needs no login. Artifik answers
// 500 for an id it does not know.
func (c *Client) Procurement(ctx context.Context, id int) (*Procurement, error) {
	var data struct {
		Procurement *procurementWire `json:"procurement"`
	}
	path := fmt.Sprintf("/procurements/%d.data", id)
	if err := c.loader(ctx, path, "routes/procurements_.$proc_id", nil, false, &data); err != nil {
		return nil, err
	}
	if data.Procurement == nil {
		return nil, ctlkit.NotFoundf("%s has no procurement %d", service, id)
	}
	return data.Procurement.procurement(), nil
}

// Document is the competition document of a procurement, as Artifik keeps
// it in its own editor: the rules of the competition, what the buyer asks
// for, and how the bids are weighed.
type Document struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Text string `json:"text"`
	Asks int    `json:"asks"`
}

// Document reads the competition document of a procurement. Text is
// Markdown, and empty when the procurement has none.
func (c *Client) Document(ctx context.Context, id int) (*Document, error) {
	var data struct {
		Procurement *procurementWire `json:"procurement"`
	}
	path := fmt.Sprintf("/procurements/%d.data", id)
	if err := c.loader(ctx, path, "routes/procurements_.$proc_id", nil, false, &data); err != nil {
		return nil, err
	}
	if data.Procurement == nil {
		return nil, ctlkit.NotFoundf("%s has no procurement %d", service, id)
	}
	doc := rawDoc(data.Procurement.InformationDoc)
	text := docMarkdown(doc)
	return &Document{ID: id, Name: data.Procurement.Name, Text: text, Asks: strings.Count(text, "[Supplier to give:")}, nil
}

// Question is a question a supplier asked and the buyer answered in public.
type Question struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Question  string `json:"question"`
	Answer    string `json:"answer"`
	Published string `json:"published"`
	Files     int    `json:"files"`
}

// Questions reads the public questions and answers of a procurement.
func (c *Client) Questions(ctx context.Context, id int) ([]Question, error) {
	var data struct {
		QAs []struct {
			ID            int    `json:"id"`
			Title         string `json:"title"`
			Question      string `json:"question"`
			Answer        string `json:"answer"`
			PublishedDate string `json:"publishedDate"`
			Files         []any  `json:"files"`
		} `json:"qas"`
	}
	path := fmt.Sprintf("/procurements/%d/qas.data", id)
	if err := c.loader(ctx, path, "routes/procurements_.$proc_id.qas", nil, false, &data); err != nil {
		return nil, err
	}
	questions := make([]Question, 0, len(data.QAs))
	for _, q := range data.QAs {
		questions = append(questions, Question{
			ID: q.ID, Title: q.Title, Question: text(q.Question), Answer: text(q.Answer),
			Published: q.PublishedDate, Files: len(q.Files),
		})
	}
	return questions, nil
}

// Download fetches one attachment of a procurement by its name. Artifik
// hands out an address on S3 that works for five minutes, and the file is
// fetched from there without the login.
func (c *Client) Download(ctx context.Context, id int, name string) ([]byte, error) {
	form := url.Values{
		"endpoint":  {"publicProcurement"},
		"intent":    {"download"},
		"file_name": {name},
		"proc_id":   {strconv.Itoa(id)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/manage-files", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	body, err := httpx.Do(c.HTTP, req, service, c.UserAgent)
	if err != nil {
		return nil, err
	}
	var answer struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.URL == "" {
		return nil, fmt.Errorf("%s gave no address for the file %q", service, name)
	}
	if err := c.checkFileURL(answer.URL); err != nil {
		return nil, err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, answer.URL, nil)
	if err != nil {
		return nil, err
	}
	return httpx.Do(c.HTTP, req, service, c.UserAgent)
}

// checkFileURL refuses an address that is not on Artifik's file store, so
// that an answer cannot send the tool anywhere else.
func (c *Client) checkFileURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != c.FileStore.Scheme || !strings.HasSuffix(u.Host, c.FileStore.Host) {
		return fmt.Errorf("%s gave a file address outside its file store", service)
	}
	return nil
}

// Mine is one of the user's own procurements: one they bid on or were
// invited to.
type Mine struct {
	Summary
	Status         string `json:"status,omitempty"`
	SupplierStatus string `json:"supplierStatus,omitempty"`
	TenderID       int    `json:"tenderId,omitempty"`
}

// MinePage is a page of the user's own procurements.
type MinePage struct {
	Items []Mine
	Total int
}

// Mine reads the user's own procurements, newest first as Artifik gives
// them. It needs the login.
func (c *Client) Mine(ctx context.Context, size int) (*MinePage, error) {
	query := url.Values{"pageSize": {strconv.Itoa(size)}}
	var data struct {
		All   []summaryWire `json:"allProcurements"`
		Total int           `json:"total"`
	}
	if err := c.loader(ctx, "/_root.data", "routes/_index", query, true, &data); err != nil {
		return nil, err
	}
	page := &MinePage{Items: make([]Mine, 0, len(data.All)), Total: data.Total}
	for _, w := range data.All {
		m := Mine{Summary: w.summary(), Status: w.Status}
		if w.TenderID != nil {
			m.TenderID = *w.TenderID
		}
		if w.SupplierInfo != nil {
			m.SupplierStatus = w.SupplierInfo.SupplierStatus
		}
		page.Items = append(page.Items, m)
	}
	return page, nil
}

// loader reads the data of one route of the web client into into. With
// authorized set it sends the login cookie, and renews the login once when
// Artifik turns it away.
func (c *Client) loader(ctx context.Context, path, route string, query url.Values, authorized bool, into any) error {
	send := func() error {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("_routes", route)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		if authorized && c.Token != "" {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: c.Token})
		}
		body, err := httpx.Do(c.HTTP, req, service, c.UserAgent)
		if err != nil {
			return err
		}
		return decodeRoute(body, route, into)
	}
	err := send()
	if !authorized || c.Renew == nil || !refused(err) {
		return err
	}
	token, renewErr := c.Renew(ctx)
	if renewErr != nil {
		return renewErr
	}
	c.Token = token
	return send()
}

// refused says whether Artifik turned the login away: a redirect to the
// login page, or a 401.
func refused(err error) bool {
	_, redirect := err.(errRedirect)
	return redirect || httpx.Status(err) == http.StatusUnauthorized
}

func decodeRoute(body []byte, route string, into any) error {
	root, err := decodeTurbo(body)
	if err != nil {
		if _, ok := err.(errRedirect); ok {
			return err
		}
		return fmt.Errorf("%s: read the answer for %s: %w", service, route, err)
	}
	var routes map[string]struct {
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(root, &routes); err != nil {
		return fmt.Errorf("%s: read the answer for %s: %w", service, route, err)
	}
	r, ok := routes[route]
	if !ok || len(r.Data) == 0 || string(r.Data) == "null" {
		return fmt.Errorf("%s: the answer has no data for %s", service, route)
	}
	if err := json.Unmarshal(r.Data, into); err != nil {
		return fmt.Errorf("%s: read the answer for %s: %w", service, route, err)
	}
	return nil
}

// SessionFromBrowser reads the login cookie out of the storage state a
// browser saved after the user logged in, as "playwright-cli state-save"
// writes it. Artifik logs in with an SMS code or a single sign-on.
func SessionFromBrowser(state []byte) (string, error) {
	cookies, err := httpx.BrowserCookies(state, Host)
	if err != nil {
		return "", err
	}
	if cookies[cookieName] == "" {
		return "", ctlkit.Authf("the browser state has no %s cookie for %s", cookieName, Host).
			WithHint("log in at https://%s in that browser first, and save the state while the page is open.", Host)
	}
	if accessExpiry(cookies[cookieName]).IsZero() {
		return "", ctlkit.Authf("the %s cookie holds no login", cookieName).
			WithHint("log in at https://%s in that browser first, and save the state while the page is open.", Host)
	}
	return cookies[cookieName], nil
}

// RenewSession sends the login cookie to /api/ensure-session, which answers
// with a new cookie that holds a new access token. A refused cookie is a
// 401.
func (c *Client) RenewSession(ctx context.Context, cookie string) (session.Session, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/ensure-session", nil)
	if err != nil {
		return session.Session{}, err
	}
	req.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	req.Header.Set("Accept", "application/json")
	caught := httpx.CookieCatcher{}
	client := *c.HTTP
	client.Jar = caught
	if _, err := httpx.Do(&client, req, service, c.UserAgent); err != nil {
		return session.Session{}, err
	}
	renewed := caught[cookieName]
	if accessExpiry(renewed).IsZero() {
		return session.Session{}, fmt.Errorf("%s renewed the login but gave no new %s cookie", service, cookieName)
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	return session.Session{
		AccessToken:    renewed,
		AccessExpires:  accessExpiry(renewed),
		RefreshToken:   renewed,
		RefreshExpires: now().Add(cookieLifetime),
	}, nil
}

// accessExpiry reads when the access token inside the login cookie runs
// out. The cookie is base64 JSON, a dot and a signature. Zero means the
// cookie holds no access token.
func accessExpiry(cookie string) time.Time {
	payload, _, _ := strings.Cut(cookie, ".")
	if unescaped, err := url.QueryUnescape(payload); err == nil {
		payload = unescaped
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "=")); err != nil {
			return time.Time{}
		}
	}
	var inside struct {
		AccessToken string `json:"accessToken"`
	}
	if json.Unmarshal(data, &inside) != nil || inside.AccessToken == "" {
		return time.Time{}
	}
	return session.JWTExpiry(inside.AccessToken)
}
