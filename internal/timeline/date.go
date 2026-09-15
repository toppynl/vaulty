package timeline

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Precision of an entry's date token (DESIGN.md §5.3).
type Precision string

const (
	PrecDay        Precision = "day"         // 2026-09-10
	PrecDayRange   Precision = "day-range"   // 2026-09-10/12
	PrecDecade     Precision = "decade"      // 2026-08-2x
	PrecMonth      Precision = "month"       // 2026-08, "2026-03 (heel maand)"
	PrecMonthRange Precision = "month-range" // 2021-02/03
)

// Date is the parsed bold token of an entry line.
type Date struct {
	Raw       string    `json:"raw"`  // text between ** **, verbatim
	Key       string    `json:"key"`  // oracle sort key: YYYY-MM-DD or YYYY-MM-00
	From      string    `json:"from"` // inclusive lower bound, YYYY-MM-DD
	To        string    `json:"to"`   // inclusive upper bound, textual (may be YYYY-MM-31 / -29)
	Precision Precision `json:"precision"`
}

// Order matters and mirrors parseDateToken in scripts/lib/timeline.mjs.
var (
	reDayRange   = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})/(\d{1,2})\b`)
	reDecade     = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d)x\b`)
	reMonthRange = regexp.MustCompile(`^(\d{4})-(\d{2})/(\d{2})\b`)
	reFull       = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})\b`)
	reMonth      = regexp.MustCompile(`^(\d{4})-(\d{2})\b`)
)

// ParseDate parses the text inside a leading `- **...**`. ok=false means
// unparseable (the line then attaches to the entry above, see Parse).
func ParseDate(raw string) (Date, bool) {
	s := strings.TrimSpace(raw)
	d := Date{Raw: raw}
	if m := reDayRange.FindStringSubmatch(s); m != nil {
		end, _ := strconv.Atoi(m[4])
		d.Key = ymd(m[1], m[2], m[3])
		d.From, d.To = d.Key, ymd(m[1], m[2], fmt.Sprintf("%02d", end))
		d.Precision = PrecDayRange
		return d, true
	}
	if m := reDecade.FindStringSubmatch(s); m != nil {
		d.Key = ymd(m[1], m[2], m[3]+"0")
		from := m[3] + "0"
		if from == "00" {
			from = "01"
		}
		d.From, d.To = ymd(m[1], m[2], from), ymd(m[1], m[2], m[3]+"9")
		d.Precision = PrecDecade
		return d, true
	}
	if m := reMonthRange.FindStringSubmatch(s); m != nil {
		d.Key = ymd(m[1], m[2], "00")
		d.From, d.To = ymd(m[1], m[2], "01"), ymd(m[1], m[3], "31")
		d.Precision = PrecMonthRange
		return d, true
	}
	if m := reFull.FindStringSubmatch(s); m != nil {
		d.Key = ymd(m[1], m[2], m[3])
		d.From, d.To = d.Key, d.Key
		d.Precision = PrecDay
		return d, true
	}
	if m := reMonth.FindStringSubmatch(s); m != nil {
		d.Key = ymd(m[1], m[2], "00")
		d.From, d.To = ymd(m[1], m[2], "01"), ymd(m[1], m[2], "31")
		d.Precision = PrecMonth
		return d, true
	}
	return d, false
}

func ymd(y, m, dd string) string { return y + "-" + m + "-" + dd }

// OnOrAfter reports whether the entry's date range overlaps [since, ∞).
// since must be a full YYYY-MM-DD. So "2026-08" matches --since 2026-08-01
// and --since 2026-08-15; "2026-07" does not match --since 2026-08-01.
func (d Date) OnOrAfter(since string) bool { return d.To >= since }
