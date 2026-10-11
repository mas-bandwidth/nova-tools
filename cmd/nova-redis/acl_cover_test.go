package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/pkg/redisacl"

	"github.com/stretchr/testify/assert"
)

// aclClient is the store under aclStore: it answers the exact command lines
// aclStore sends and records every one, in order, so a test pins the shape
// of the round trips. Pipelined commands are answered by command line from
// replies and cmdErrs; a line the case does not script answers a nil value,
// what parseGetUser reads as a user the store lacks. execErr is what the
// Exec of each pipeline in turn answers beyond the commands: nil stands for
// an answered round trip. Do answers the one reply the case scripts. Only
// Pipeline and Do are implemented: any other method panics on the nil
// embedded client, so this fake cannot drift beyond what aclStore sends.
type aclClient struct {
	redis.UniversalClient

	replies map[string]any
	cmdErrs map[string]error
	execErr []error

	doVal string
	doErr error

	sent  []string
	execs int
}

func (c *aclClient) Pipeline() redis.Pipeliner { return &aclPipe{c: c} }

func (c *aclClient) Do(ctx context.Context, args ...any) *redis.Cmd {
	c.sent = append(c.sent, aclLine(args))
	cmd := redis.NewCmd(ctx, args...)
	if c.doErr != nil {
		cmd.SetErr(c.doErr)
		return cmd
	}
	cmd.SetVal(c.doVal)
	return cmd
}

// aclPipe queues commands against its client; Exec hands back the queued
// commands and the transport failure scripted for that round trip.
type aclPipe struct {
	redis.Pipeliner
	c    *aclClient
	cmds []redis.Cmder
}

func (p *aclPipe) Do(ctx context.Context, args ...any) *redis.Cmd {
	line := aclLine(args)
	p.c.sent = append(p.c.sent, line)
	cmd := redis.NewCmd(ctx, args...)
	if err, scripted := p.c.cmdErrs[line]; scripted {
		cmd.SetErr(err)
	} else {
		cmd.SetVal(p.c.replies[line])
	}
	p.cmds = append(p.cmds, cmd)
	return cmd
}

func (p *aclPipe) Exec(context.Context) ([]redis.Cmder, error) {
	cmds := p.cmds
	p.cmds = nil
	var err error
	if p.c.execs < len(p.c.execErr) {
		err = p.c.execErr[p.c.execs]
	}
	p.c.execs++
	return cmds, err
}

func aclLine(args []any) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprint(a)
	}
	return strings.Join(parts, " ")
}

var _ aclServer = aclStore{}

// TestAclCoverStoreRead: aclStore.Read answers every named user plus
// default's live ACL, the store's user names and its catalog in two round
// trips: the first pipelines ACL CAT, ACL USERS and one ACL GETUSER per
// user, the second ACL CAT for every category, whose names the catalog keys
// lower-case. A refusal of the pipeline, or an answer that is not a list,
// comes back as one error naming the command. No store is opened: the fake
// answers what a live one would.
func TestAclCoverStoreRead(t *testing.T) {
	t.Parallel()
	first := []string{"ACL CAT", "ACL USERS", "ACL GETUSER coordinator", "ACL GETUSER default"}
	coordinator := []any{
		"flags", []any{"on"},
		"keys", "~coordinator:*",
		"channels", "&*",
		"commands", "-@all +get",
		"selectors", []any{[]any{"~c:* +ping"}},
	}
	full := map[string]any{
		"ACL CAT":                 []any{"Read", "Write"},
		"ACL USERS":               []any{"coordinator", "legacy"},
		"ACL GETUSER coordinator": coordinator,
		"ACL CAT Read":            []any{"get"},
		"ACL CAT Write":           []any{"set"},
	}
	cases := []struct {
		name         string
		replies      map[string]any
		cmdErrs      map[string]error
		execErr      []error
		wantLive     map[string]redisacl.Live
		wantEveryone []string
		wantCat      redisacl.Catalog
		wantSent     []string
		wantExecs    int
		wantErr      string
	}{
		{"the live ACL, the names and the catalog", full,
			map[string]error{"ACL GETUSER default": redis.Nil}, nil,
			map[string]redisacl.Live{
				"coordinator": {Exists: true, On: true, Keys: "~coordinator:*", Channels: "&*", Commands: "-@all +get", Selectors: 1},
				"default":     {},
			},
			[]string{"coordinator", "legacy"},
			redisacl.Catalog{"read": {"get"}, "write": {"set"}},
			append(append([]string{}, first...), "ACL CAT Read", "ACL CAT Write"), 2, ""},
		{"the round trip does not answer", full,
			map[string]error{"ACL GETUSER default": redis.Nil},
			[]error{errors.New("the store did not answer")},
			nil, nil, nil, first, 1, "the store did not answer"},
		{"the second round trip does not answer", full,
			map[string]error{"ACL GETUSER default": redis.Nil},
			[]error{nil, errors.New("the store did not answer the catalog")},
			nil, nil, nil,
			append(append([]string{}, first...), "ACL CAT Read", "ACL CAT Write"), 2, "the store did not answer the catalog"},
		{"ACL CAT answers a name, not a list", map[string]any{"ACL CAT": 7, "ACL USERS": []any{"coordinator"}},
			nil, nil,
			nil, nil, nil, first, 1, "ACL CAT: "},
		{"ACL USERS answers a name, not a list", map[string]any{"ACL CAT": []any{"Read"}, "ACL USERS": "coordinator"},
			nil, nil,
			nil, nil, nil, first, 1, "ACL USERS: "},
		{"a category answers no command list", func() map[string]any {
			m := map[string]any{}
			for k, v := range full {
				m[k] = v
			}
			m["ACL CAT Read"] = 7
			return m
		}(), map[string]error{"ACL GETUSER default": redis.Nil}, nil,
			nil, nil, nil,
			append(append([]string{}, first...), "ACL CAT Read", "ACL CAT Write"), 2, "ACL CAT Read: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &aclClient{replies: tc.replies, cmdErrs: tc.cmdErrs, execErr: tc.execErr}
			srv := aclStore{c: c}
			live, everyone, cat, err := srv.Read(context.Background(), []string{"coordinator"})
			if tc.wantErr != "" {
				if assert.Error(t, err, "Read answered %v, %v, %v; want the error %q", live, everyone, cat, tc.wantErr) {
					assert.ErrorContains(t, err, tc.wantErr, "Read answered %v; want it to name the failed command", err)
				}
				assert.Nil(t, live, "Read answered a live ACL beside the error")
				assert.Nil(t, everyone, "Read answered names beside the error")
				assert.Nil(t, cat, "Read answered a catalog beside the error")
			} else {
				if !assert.NoError(t, err, "Read: %v", err) {
					return
				}
				assert.Equal(t, tc.wantLive, live, "Read's live ACL")
				assert.Equal(t, tc.wantEveryone, everyone, "Read's user names")
				assert.Equal(t, tc.wantCat, cat, "Read's catalog: keys are lower-case, as Compare reads them")
			}
			assert.Equal(t, tc.wantSent, c.sent, "commands sent, in order")
			assert.Equal(t, tc.wantExecs, c.execs, "round trips: want %d", tc.wantExecs)
		})
	}
}

// TestAclCoverStoreSetUser: aclStore.SetUser sends ACL SETUSER, the user's
// name and then every rule in order, and answers the store's error when the
// store refuses the command.
func TestAclCoverStoreSetUser(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		doVal    string
		doErr    error
		rules    []string
		wantSent string
		wantErr  string
	}{
		{"every rule, in order", "OK", nil,
			[]string{"on", ">secret", "~coordinator:*", "-@all", "+get"},
			"ACL SETUSER coordinator on >secret ~coordinator:* -@all +get", ""},
		{"a user reset with no rules", "OK", nil, nil, "ACL SETUSER coordinator", ""},
		{"the store refuses", "", storeReply("NOPERM User admin has no permissions to run the 'acl|setuser' command"),
			[]string{"on"}, "ACL SETUSER coordinator on", "NOPERM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &aclClient{doVal: tc.doVal, doErr: tc.doErr}
			srv := aclStore{c: c}
			err := srv.SetUser(context.Background(), "coordinator", tc.rules)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr, "SetUser: %v; want the store's refusal", err)
			} else {
				assert.NoError(t, err, "SetUser: %v", err)
			}
			assert.Equal(t, []string{tc.wantSent}, c.sent, "commands sent")
		})
	}
}

// TestAclCoverStoreSave: aclStore.Save sends ACL SAVE and says whether the
// store kept an ACL file: a store that keeps none is saved=false with no
// error, because its users live on; any other failure is the error.
func TestAclCoverStoreSave(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		doVal     string
		doErr     error
		wantSaved bool
		wantErr   string
	}{
		{"the store writes its ACL file", "OK", nil, true, ""},
		{"the store keeps no ACL file", "",
			storeReply("ERR This Redis instance is not configured to use an ACL file. You may want to specify users via the ACL SETUSER command and then issue a CONFIG REWRITE (assuming you have a Redis configuration file set) in order to store users in the Redis configuration."),
			false, ""},
		{"the store fails", "", storeReply("ERR Saving the ACL to disk failed"), false, "Saving the ACL to disk failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &aclClient{doVal: tc.doVal, doErr: tc.doErr}
			srv := aclStore{c: c}
			saved, err := srv.Save(context.Background())
			assert.Equal(t, tc.wantSaved, saved, "Save answered %t, %v; want saved=%t", saved, err, tc.wantSaved)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr, "Save: %v; want the store's error", err)
			} else {
				assert.NoError(t, err, "Save: %v", err)
			}
			assert.Equal(t, []string{"ACL SAVE"}, c.sent, "commands sent")
		})
	}
}
