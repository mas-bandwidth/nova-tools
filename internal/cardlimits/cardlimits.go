// Package cardlimits holds the two sizes a card's brief is held to, in one place that
// nothing is behind: no redis client, no table layer, no store. The sprint's store refuses a
// brief over MaxBriefBytes, and the card lint advises a brief stays under BriefAdvisoryBytes
// and says where the refusal is, so both read the numbers here and cannot disagree.
package cardlimits

// MaxBriefBytes is the size `nova-sprint add` refuses a brief over (16 KiB). A brief is a
// child's whole brief, so it carries every detail the child needs; the bound sits above the
// lint's advice (BriefAdvisoryBytes) so a brief the lint passes is never refused for size,
// and well under the table layer's field bound (64 KiB), which holds a brief whole.
const MaxBriefBytes = 16 << 10

// BriefAdvisoryBytes is the size the card lint (`nova-worker lint --card`) advises a card
// stays under: past it a model stops reading the card in one window. It is advice and
// refuses nothing; MaxBriefBytes is the refusal.
const BriefAdvisoryBytes = 12000
