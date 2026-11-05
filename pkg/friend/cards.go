package friend

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HeldCard is one card on the friend's row as the server answers friend cards: the card,
// its job name, its column (ready or working) and its BRIEF.md whole, with its packet (its
// kind, work or read, the branch it is pushed to, its tier, attempt, generation and epoch,
// and the repository and base its checkout is staged from).
type HeldCard struct {
	Card    string `json:"card"`
	Job     string `json:"job"`
	Col     string `json:"col"`
	Kind    string `json:"kind,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Tier    string `json:"tier,omitempty"`
	Stream  string `json:"stream,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	Gen     int    `json:"gen,omitempty"`
	Epoch   uint64 `json:"epoch"`
	Repo    string `json:"repo,omitempty"`
	Base    string `json:"base,omitempty"`
	Brief   string `json:"brief"`
	// Lane is the lane of her row the server says holds the card (take --lane), 0 for none.
	Lane int `json:"lane,omitempty"`
}

// HeldAnswer is the server's answer to friend cards <friend>: every card on her row, the
// ready ones dealt behind her working ones and her reads among them.
type HeldAnswer struct {
	Friend string     `json:"friend"`
	Cards  []HeldCard `json:"cards"`
}

// FriendCardsArgv is the worker verb that asks the sprint server for her held cards.
func FriendCardsArgv(friend string) []string { return []string{"friend", "cards", friend, "--json"} }

// ParseHeld reads the server's answer to FriendCardsArgv: its one JSON line, for this friend.
func ParseHeld(friend, out string) ([]HeldCard, error) {
	var a HeldAnswer
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &a); err != nil {
		return nil, fmt.Errorf("friend cards: the answer is not JSON: %v", err)
	}
	if a.Friend != friend {
		return nil, fmt.Errorf("friend cards: the answer is %q's, not %q's", a.Friend, friend)
	}
	return a.Cards, nil
}
