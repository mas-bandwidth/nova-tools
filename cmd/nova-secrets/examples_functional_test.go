//go:build functional

package main

import (
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
)

// TestTheHelpExamplesAreWhatTheSeatsVerbsPrint: names, check, exec, place, placed and seat
// add, in the banner's order, in one sitting.
func TestTheHelpExamplesAreWhatTheSeatsVerbsPrint(t *testing.T) {
	t.Parallel()
	h := newSittingHome(t)
	placed := "machine=bench-a secret=DEEPSEEK_API_KEY path=/home/bench/.config/nova-secrets/DEEPSEEK_API_KEY.env file=ada.yaml head=- blob=" +
		blobOf(t, filepath.Join(h.home, "secrets", "ada.yaml")) + " stamp=-"
	runSitting(t, h, bannerExamples(t), []onboarding.Step{
		{Line: "$ nova-secrets names --store ./secrets --as ada", Want: []string{
			"SECRETS NAME key=DEEPSEEK_API_KEY clear=false",
			"SECRETS NAME key=GH_TOKEN clear=false",
			"SECRETS NAME key=NOVA_REDIS_BENCH_PASSWORD clear=false",
			"SECRETS NAMES OK as=ada keys=3 shown=3 sealed=3 clear=0",
		}},
		{Line: "$ nova-secrets check --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops", Want: []string{
			"SECRETS CHECK OK as=ada recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=0 head=-",
		}},
		{Line: "$ nova-secrets exec --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user", Want: []string{
			"gh api user token=set",
			"! SECRETS EXEC OK as=ada keys=1 only=1 required=1 file=secrets/ada.yaml head=- cmd=gh",
		}},
		{Line: "$ nova-secrets place --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --machine bench-a --secret DEEPSEEK_API_KEY --machines ./fleet.tsv", Want: []string{
			"SECRETS PLACE OK " + placed,
		}},
		{Line: "$ nova-secrets placed --machine bench-a", Want: []string{
			"SECRETS PLACED OK machine=bench-a count=1",
			"SECRETS PLACED ITEM " + placed,
		}},
		{Line: "$ nova-secrets seat add --store ./secrets --as bo --pub $BO_PUB --from ada --only GH_TOKEN,DEEPSEEK_API_KEY --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops", Want: []string{
			"SECRETS SEAT ADD NEXT: commit .sops.yaml and bo.yaml on a branch and open the pull request the store's gate reviews",
			"SECRETS SEAT ADD OK as=bo from=ada keys=2 file=bo.yaml rule=2",
			"! seat add: reading ada.yaml",
			"! seat add: writing the rule for bo.yaml into .sops.yaml",
			"! seat add: encrypting 2 value(s) to the new seat's recipients",
		}},
	})
}
