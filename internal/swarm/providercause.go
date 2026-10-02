package swarm

import (
	"cmp"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	novalog "github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// WHAT FAILED IS ON THE RECORD (docs/SPEC-CARD-CONTRACT.md section 4, the provider failure).
//
// Every provider failure was recorded as one word whatever its cause: a model id the
// provider does not know, a key it refuses, an account out of credit, a rate limit and an
// outage all read `PROVIDER-5XX`, and the provider's own words were cut off behind the
// harness log's session fields. Each of those needs a different action -- a wrong id is a
// config error to fix, a limit rests the route, an outage retries -- so the record carries
// the cause: a class, the HTTP status when there is one, and the provider's own message,
// one line, bounded, with any secret-shaped value removed. The class is a record: it
// changes no judgment (a provider failure is still never the card's).

// The classes of a provider failure.
const (
	CauseUnknownModel = "unknown-model" // the provider does not know or serve the model id: a config error
	CauseAuth         = "auth"          // the key is missing, wrong or refused
	CauseCredit       = "out-of-credit" // the account has no credit or quota left
	CauseRateLimited  = "rate-limited"  // a rate limit, the provider's or upstream of it
	Cause5xx          = "provider-5xx"  // the provider's own server error or outage
	CauseTimeout      = "timeout"       // the provider took too long to answer
	CauseOther        = "other"         // none of the above, or the harness recorded no cause
)

// providerMsgBytes bounds the message a cause carries, so the class, the status and the
// message's start all survive the sprint's cut of its provider line
// (internal/sprint MaxProviderErrorBytes).
const providerMsgBytes = 120

// ProviderCause is what one provider failure was, as the harness's own record says it.
type ProviderCause struct {
	Class   string // one of the Cause* classes
	Status  int    // the HTTP status, 0 when the record names none
	Message string // the provider's own words: one line, bounded, secrets removed
}

// Reason is the cause as the finish carries it:
//
//	provider: class=<class> status=<status|-> msg=<message>
//
// msg is last and carries the rest of the line.
func (c ProviderCause) Reason() string {
	status := Dash
	if c.Status > 0 {
		status = strconv.Itoa(c.Status)
	}
	return "provider: class=" + oneline.Field(cmp.Or(c.Class, CauseOther)) + " status=" + status + " msg=" + cmp.Or(c.Message, Dash)
}

var (
	causeCreditRE = regexp.MustCompile(`(?i)insufficient[ _-]?(?:credit|balance|funds|quota)|out of credit|credit balance|payment required|no credits|more credits|exceeded your current quota|billing`)
	causeRateRE   = regexp.MustCompile(`(?i)rate[ _-]?limit|too many requests`)
	causeAuthRE   = regexp.MustCompile(`(?i)unauthori[sz]ed|invalid[ _-]?api[ _-]?key|incorrect api key|authentication|api key (?:is )?(?:missing|invalid|not valid)|forbidden|ProviderAuthError`)
	causeTimeRE   = regexp.MustCompile(`(?i)timed out|timeout`)
	cause5xxRE    = regexp.MustCompile(`(?i)server_error|internal_error|internal server error|bad gateway|service unavailable|overloaded|endpoint is unavailable|stream error|h2 protocol error`)
	causeModelRE  = regexp.MustCompile(`(?i)unknown model|invalid model|model_not_found|ModelNotFound|no endpoints found|not a valid model|model\b[^.]{0,60}\b(?:not found|does not exist|not supported|unsupported|is not available)`)
)

// ClassifyProvider is the class of a provider failure from its HTTP status (0 when none)
// and its words (the message and whatever body came with it). The status decides where it
// is unambiguous; the words decide the rest, and refine a 429 that is a quota and a
// 4xx that is the provider's own server error.
func ClassifyProvider(status int, text string) string {
	switch {
	case status == 402:
		return CauseCredit
	case status == 429:
		if causeCreditRE.MatchString(text) {
			return CauseCredit // a 429 that says the quota is spent is no rate limit
		}
		return CauseRateLimited
	case status == 401 || status == 403:
		if causeCreditRE.MatchString(text) {
			return CauseCredit
		}
		return CauseAuth
	case status == 408 || status == 504:
		return CauseTimeout
	case status >= 500 && status <= 599:
		return Cause5xx
	case status >= 400 && status <= 499:
		switch {
		case causeCreditRE.MatchString(text):
			return CauseCredit
		case causeModelRE.MatchString(text) || status == 404:
			return CauseUnknownModel
		case cause5xxRE.MatchString(text):
			return Cause5xx // the body says it is the provider's server error, whatever the status
		}
		return CauseOther
	}
	switch {
	case causeCreditRE.MatchString(text):
		return CauseCredit
	case causeRateRE.MatchString(text):
		return CauseRateLimited
	case causeAuthRE.MatchString(text):
		return CauseAuth
	case causeModelRE.MatchString(text):
		return CauseUnknownModel
	case causeTimeRE.MatchString(text):
		return CauseTimeout
	case cause5xxRE.MatchString(text):
		return Cause5xx
	}
	return CauseOther
}

// textStatusRE is an HTTP error status a line names: `statusCode=503`, `error.error.code=504`,
// `status 502`, `HTTP 529`. Only 400..599: a provider failure's status is an error's, and a
// URL's `http://127.0.0.1` is no status.
var textStatusRE = regexp.MustCompile(`(?i)\b(?:status(?:code)?|code|http)\W{0,3}([45]\d\d)\b`)

// keyTailRE is where a provider starts echoing a key in its own words (`key: <value>`,
// `key <value>`): everything after it is dropped, since a short or lowercase-hex key is no
// shape the redactor knows.
var keyTailRE = regexp.MustCompile(`(?i)\bkey(?::|\s)`)

// textMessageRE is the provider's own words on a harness log line, best first: the error's
// message, the error itself, then the line's message.
var textMessageRE = []*regexp.Regexp{
	regexp.MustCompile(`\berror\.error\.message="((?:[^"\\]|\\.)*)"`),
	regexp.MustCompile(`\berror\.error="((?:[^"\\]|\\.)*)"`),
	regexp.MustCompile(`\berror\.message="((?:[^"\\]|\\.)*)"`),
	regexp.MustCompile(`"message"\s*:\s*"((?:[^"\\]|\\.)*)"`),
	regexp.MustCompile(`\bmessage="((?:[^"\\]|\\.)*)"`),
}

// envelopeNameRE is the name of a harness's JSON error envelope (`"name": "UnknownError"`).
var envelopeNameRE = regexp.MustCompile(`"name"\s*:\s*"([A-Za-z]+Error)"`)

// apiCallPrefixRE is the SDK's own wrapper word in front of the provider's message.
var apiCallPrefixRE = regexp.MustCompile(`^AI_[A-Za-z]+Error:\s*`)

// harnessModelNotFoundRE is the harness's own catalog refusing the model before any
// request (opencode's printed `error="ProviderModelNotFoundError: Model not found:
// <model>. Did you mean ..."`): the model it names.
var harnessModelNotFoundRE = regexp.MustCompile(`ProviderModelNotFoundError:\s*Model not found:\s*(\S+?)\.(?:\s|"|$)`)

// CauseFromText is the cause a line of text records: a harness log's error line, or the
// last lines of the harness's output (its error envelope). The status is the one the text
// names, the message the provider's own words in it, else the text itself.
func CauseFromText(text string) ProviderCause {
	if m := harnessModelNotFoundRE.FindStringSubmatch(text); m != nil {
		return ProviderCause{Class: CauseUnknownModel, Message: causeMessage("model not found in the harness catalog: " + m[1])}
	}
	status := 0
	if m := textStatusRE.FindStringSubmatch(text); m != nil {
		status, _ = strconv.Atoi(m[1]) // ignored: the pattern holds three digits
	}
	msg := text
	for _, re := range textMessageRE {
		if m := re.FindStringSubmatch(text); m != nil {
			msg = m[1]
			break
		}
	}
	msg = apiCallPrefixRE.ReplaceAllString(msg, "")
	if m := envelopeNameRE.FindStringSubmatch(text); m != nil && !strings.Contains(msg, m[1]) {
		msg = m[1] + ": " + msg
	}
	return ProviderCause{Class: ClassifyProvider(status, text), Status: status, Message: causeMessage(msg)}
}

// sessionError is the error a harness's session keeps on the message the provider failed:
// the error's name and, for an API error, the provider's status, message and body.
type sessionError struct {
	Name string `json:"name"`
	Data struct {
		Message      string `json:"message"`
		StatusCode   int    `json:"statusCode"`
		ResponseBody string `json:"responseBody"`
	} `json:"data"`
}

// CauseFromSessionError is the cause the harness's session database recorded on the
// message the provider failed (its `error` JSON); ok is false when raw holds no error.
func CauseFromSessionError(raw string) (ProviderCause, bool) {
	var e sessionError
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &e); err != nil || (e.Name == "" && e.Data.Message == "") {
		return ProviderCause{}, false
	}
	msg := e.Data.Message
	if msg == "" {
		msg = e.Name
	}
	return ProviderCause{
		Class:   ClassifyProvider(e.Data.StatusCode, e.Name+" "+e.Data.Message+" "+e.Data.ResponseBody),
		Status:  e.Data.StatusCode,
		Message: causeMessage(msg),
	}, true
}

// causeMessage is a message as a cause carries it: unescaped from the log's quoting, one
// line, everything after a `key:` or `key ` dropped (keyTailRE), every other secret-shaped
// value removed (internal/log Redact), cut to providerMsgBytes with the cut said.
func causeMessage(s string) string {
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.Join(strings.Fields(s), " ")
	if loc := keyTailRE.FindStringIndex(s); loc != nil {
		s = s[:loc[0]+len("key")] + " " + novalog.Redacted
	}
	return oneline.Cap(novalog.Redact(s), providerMsgBytes)
}
