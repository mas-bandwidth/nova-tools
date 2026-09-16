package swarm

import (
	"strings"
	"testing"
)

// THE SETUP TEMPLATE IS #184'S NEAR-TERM ENDPOINT: review and agreement only. The issue
// asks for generic configuration examples and an agreement/evidence template "rather than
// private security details", and this repository's one mechanism for a practice a caller
// must not retype is a template in the binary (WORKER-CARDS.md: "a promoted practice lands
// in nova-swarm template"). Every wanted string below is a sentence of that issue: the
// attributed proposal, the friend's own agreement half where missing feedback is pending
// and never assent, the guarantee table that separates what the OS wall enforces from what
// only a cooperating harness does, the generic wall/fence/seat/launcher examples with
// placeholder values, and the two validation runs on synthetic secrets and disposable
// repositories. And the negative half, which is the issue's own hard line: no friend's
// name, no live bench path, no key-shaped or token-shaped string -- a template that
// published a private detail would be the one artifact this issue exists to keep private.
func TestTheSetupTemplateIsTheAgreementFormNotThePrivateConfig(t *testing.T) {
	body, err := Template("setup")
	if err != nil {
		t.Fatalf("template setup: %v", err)
	}
	for _, want := range []string{
		"setup — one friend's safety setup, proposed, reviewed, agreed (#184)",
		"proposal by: <",
		"reviewed with: <",
		"status: agree | alternative | decline | pending",
		"missing feedback is pending, never assent",
		"| guarantee | who enforces it | supported here | evidence |",
		"the OS wall",
		"a cooperating harness",
		"nova-sandbox --read",
		`"external_directory": "deny"`,
		"the harness this friend chose",
		"the model this friend chose",
		"supplies no account access",
		"synthetic secrets and disposable repositories",
		"a denied destructive operation and successful permitted work",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the setup template does not carry %q", want)
		}
	}
	lower := strings.ToLower(body)
	for _, private := range []string{"rowan", "freddy", "stella", "emma", "johnny", "glenn", "deepseek", "age1", "sk-", "ghp_", "mas-bandwidth", "/users/", "/opt/homebrew"} {
		if strings.Contains(lower, private) {
			t.Errorf("the setup template publishes a private or live detail: %q", private)
		}
	}
	// The banner and the refusal name the same set, and that set is TemplateNames: a name
	// Template answers to that the list omits is the drift that lost `worker` once.
	if names := strings.Join(TemplateNames(), ","); !strings.Contains(names, "setup") {
		t.Errorf("TemplateNames must carry setup so the banner and the refusal name the same set: %s", names)
	}
	// It is a form, not a task's conditions: `add --template setup` is refused the way
	// `result` is, because wrapping a task inside an agreement form produces a prompt that
	// is neither.
	if _, err := WrapTemplate("setup", 3, []byte("a task")); err == nil {
		t.Error("`setup` is the agreement form and not a task template; add --template setup must be refused the way `result` is")
	}
}
