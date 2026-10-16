package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/langtind/ctlkit"

	"github.com/langtind/artifikctl/internal/artifik"
)

// Output is Markdown, which reads well both in a terminal and when an agent
// relays it into a chat. Names from the service are printed on one line, so
// that a line break in a name cannot pass for structure, and longer texts
// are quoted.

// shortText is how much of a description a list shows.
const shortText = 200

func (a *App) renderProcurements(w io.Writer, list procurementList, open bool) {
	title := "# Procurements"
	if open {
		title = "# Open procurements"
	}
	fmt.Fprintln(w, title)
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNone found.")
		return
	}
	fmt.Fprintln(w)
	for _, p := range list.Items {
		fmt.Fprintf(w, "- %s\n", oneLine(p.Name))
		field(w, "id", strconv.Itoa(p.ID))
		field(w, "Buyer", p.Buyer)
		field(w, "Type", p.Type)
		field(w, "Phase", p.Phase)
		if p.Deadline != "" {
			field(w, "Next deadline", a.local(p.Deadline)+" ("+p.DeadlineType+")")
		}
		if p.Budget > 0 {
			field(w, "Budget", amount(p.Budget))
		}
		field(w, "Place", p.Place)
		if p.Description != "" {
			field(w, "About", short(p.Description))
		}
	}
	fmt.Fprintf(w, "\nShowing %d. Artifik has %d for this search", len(list.Items), list.Total)
	if open {
		fmt.Fprint(w, ", open or not")
	}
	fmt.Fprintln(w, ".")
	switch {
	case list.NextPage > 0:
		fmt.Fprintf(w, "There may be more. Go on with --page %d.\n", list.NextPage)
	case list.Truncated:
		fmt.Fprintln(w, "There are more. Raise --limit to see them.")
	}
}

func (a *App) renderProcurement(w io.Writer, page procurementPage) {
	p := page.Procurement
	fmt.Fprintf(w, "# %s\n\n", oneLine(p.Name))
	fmt.Fprintf(w, "id: %d\n", p.ID)
	line(w, "Buyer", p.Buyer)
	line(w, "Type", p.Type)
	line(w, "Procedure", p.Procedure)
	line(w, "Phase", p.Phase)
	line(w, "Regulation", p.Regulation)
	line(w, "Threshold", p.Threshold)
	line(w, "Category", p.Category)
	if p.TotalValue > 0 {
		line(w, "Estimated value", strings.TrimSpace(amount(p.TotalValue)+" "+p.Currency))
	}
	line(w, "Main CPV", p.MainCPV)
	if len(p.OtherCPV) > 0 {
		line(w, "Other CPV", strings.Join(p.OtherCPV, ", "))
	}
	if len(p.Places) > 0 {
		line(w, "Places", strings.Join(p.Places, ", "))
	}
	line(w, "External id", p.ExternalID)
	line(w, "External page", p.ExternalURL)
	line(w, "Page", p.URL)

	if p.Description != "" {
		fmt.Fprintf(w, "\n## About\n\n%s\n", ctlkit.Quote(p.Description))
	}
	if len(p.Timeline) > 0 {
		fmt.Fprintln(w, "\n## Timeline")
		fmt.Fprintln(w)
		for _, e := range p.Timeline {
			fmt.Fprintf(w, "- %s: %s\n", oneLine(e.Name), a.local(e.Date))
		}
	}
	// One lot without a name is the procurement itself, and its parts are
	// shown as the procurement's own.
	if len(p.LotList) == 1 && p.LotList[0].Name == "" {
		renderLot(w, p.LotList[0], "##")
	} else if len(p.LotList) > 0 {
		fmt.Fprintln(w, "\n## Lots")
		for i, lot := range p.LotList {
			name := oneLine(lot.Name)
			if name == "" {
				name = fmt.Sprintf("Lot %d", i+1)
			}
			fmt.Fprintf(w, "\n### %s\n", name)
			renderLot(w, lot, "####")
		}
	}
	fmt.Fprintln(w, "\n## Attachments")
	fmt.Fprintln(w)
	if len(p.Files) == 0 {
		fmt.Fprintln(w, "None.")
	} else {
		for i, f := range p.Files {
			fmt.Fprintf(w, "%d. %s\n", i+1, oneLine(f.Name))
		}
		fmt.Fprintf(w, "\nSave one with \"%s download %d <number>\", or all with \"%s download %d --all\".\n", Name, p.ID, Name, p.ID)
	}
	if page.Questions > 0 {
		fmt.Fprintf(w, "\n## Questions\n\n%d public questions and answers. Read them with \"%s questions %d\".\n", page.Questions, Name, p.ID)
	}
}

// renderLot writes the parts of a lot under headings of the given level.
func renderLot(w io.Writer, lot artifik.Lot, level string) {
	if lot.DurationMonths > 0 || lot.EstimatedValue > 0 {
		fmt.Fprintln(w)
	}
	if lot.DurationMonths > 0 {
		fmt.Fprintf(w, "Duration: %s months\n", strconv.FormatFloat(lot.DurationMonths, 'f', -1, 64))
	}
	if lot.EstimatedValue > 0 {
		fmt.Fprintf(w, "Estimated value: %s\n", amount(lot.EstimatedValue))
	}
	if lot.Description != "" {
		fmt.Fprintf(w, "\n%s\n", ctlkit.Quote(lot.Description))
	}
	if len(lot.Qualification) > 0 {
		fmt.Fprintf(w, "\n%s Qualification requirements\n\n", level)
		for i, c := range lot.Qualification {
			fmt.Fprintf(w, "%d. %s", i+1, oneLine(c.Title))
			if c.Type != "" {
				fmt.Fprintf(w, " (%s)", c.Type)
			}
			if c.DocumentationRequired {
				fmt.Fprint(w, ", documentation required")
			}
			fmt.Fprintln(w)
			if c.Description != "" {
				fmt.Fprintf(w, "\n%s\n\n", indent(ctlkit.Quote(c.Description)))
			}
		}
	}
	if len(lot.Award) > 0 {
		fmt.Fprintf(w, "\n%s Award criteria\n\n", level)
		for _, c := range lot.Award {
			fmt.Fprintf(w, "- %s: %s %%, %s\n", oneLine(c.Title), strconv.FormatFloat(c.Percentage, 'f', -1, 64), c.Type)
		}
	}
	if lot.Specification != "" {
		fmt.Fprintf(w, "\n%s Specification\n\n%s\n", level, ctlkit.Quote(lot.Specification))
	}
}

// indent puts a block under a numbered list item.
func indent(block string) string {
	return "   " + strings.ReplaceAll(block, "\n", "\n   ")
}

func (a *App) renderQuestions(w io.Writer, id int, list questionList) {
	fmt.Fprintf(w, "# Questions about procurement %d\n", id)
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo public questions.")
		return
	}
	for _, q := range list.Items {
		fmt.Fprintf(w, "\n## %s\n\n", oneLine(q.Title))
		if q.Published != "" {
			fmt.Fprintf(w, "Published: %s\n", a.local(q.Published))
		}
		if q.Files > 0 {
			fmt.Fprintf(w, "Attachments: %d, see the page in Artifik\n", q.Files)
		}
		fmt.Fprintf(w, "\nQuestion:\n\n%s\n\nAnswer:\n\n%s\n", ctlkit.Quote(q.Question), ctlkit.Quote(q.Answer))
	}
}

func (a *App) renderMine(w io.Writer, list mineList) {
	fmt.Fprintln(w, "# My procurements")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNone.")
		return
	}
	fmt.Fprintln(w)
	for _, p := range list.Items {
		fmt.Fprintf(w, "- %s\n", oneLine(p.Name))
		field(w, "id", strconv.Itoa(p.ID))
		field(w, "Buyer", p.Buyer)
		field(w, "Phase", p.Phase)
		field(w, "My status", p.SupplierStatus)
		if p.Deadline != "" {
			field(w, "Next deadline", a.local(p.Deadline)+" ("+p.DeadlineType+")")
		}
	}
	if list.Truncated {
		fmt.Fprintf(w, "\nShowing %d of %d. Raise --limit to see more.\n", len(list.Items), list.Total)
	}
}

func (a *App) renderStatus(w io.Writer, status loginStatus) {
	switch {
	case !status.LoggedIn && status.Source == "":
		fmt.Fprintf(w, "Not logged in. Only \"mine\" needs a login: run \"%s help login\".\n", Name)
	case !status.LoggedIn:
		fmt.Fprintf(w, "The login ran out at %s. Log in again: run \"%s help login\".\n", a.local(status.RenewableUntil), Name)
	case status.Source == "environment":
		fmt.Fprintf(w, "Logged in with the cookie in %s. It is used as it is and never renewed.\n", envToken)
	default:
		fmt.Fprintln(w, "Logged in.")
		if status.AccessExpires != "" {
			fmt.Fprintf(w, "Access token until: %s\n", a.local(status.AccessExpires))
		}
		if status.RenewableUntil != "" {
			fmt.Fprintf(w, "Renews itself until: %s\n", a.local(status.RenewableUntil))
			fmt.Fprintln(w, "Every renewal moves that date, so the login lasts as long as it is used now and then.")
		}
	}
}

// local shows a time from the service in the user's zone, still as RFC 3339. A
// value that is not a time is shown as it is.
func (a *App) local(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.In(a.Zone).Format(time.RFC3339)
}

// field writes one indented line under a list item, and nothing for an
// empty value.
func field(w io.Writer, label, value string) {
	if value != "" {
		fmt.Fprintf(w, "  %s: %s\n", label, oneLine(value))
	}
}

// line writes one line of a page, and nothing for an empty value.
func line(w io.Writer, label, value string) {
	if value != "" {
		fmt.Fprintf(w, "%s: %s\n", label, oneLine(value))
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func short(s string) string {
	s = oneLine(s)
	if r := []rune(s); len(r) > shortText {
		return strings.TrimSpace(string(r[:shortText])) + "..."
	}
	return s
}

// amount writes a number with a space between the thousands, as Artifik
// shows it.
func amount(v float64) string {
	digits := strconv.FormatFloat(v, 'f', -1, 64)
	whole, frac, _ := strings.Cut(digits, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	if frac != "" {
		b.WriteString("," + frac)
	}
	return b.String()
}
