package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// box is a rented machine as its provider reports it. State is running, deploying (Latitude, before it is on),
// pending, stopping, stopped (AWS), or gone (deleted, terminated, or never there).
type box struct {
	ID, Name, State, Address string
	Request                  string  // AWS: the spot request that launched it, "" for on-demand
	MAC                      string  // Latitude: the ETH0 MAC, listed while the box deploys
	Price                    float64 // dollars an hour as the provider gives it (Latitude), 0 when it does not
}

// request is an AWS spot request: open (no box yet), active (its box runs), or closed, cancelled, failed (dead).
type request struct{ ID, State, Box string }

func (r request) live() bool { return r.State == "open" || r.State == "active" }

// rentSpec is what rent asks a provider for.
type rentSpec struct {
	Name, Plan, Site, Image string
	Spot                    bool
	// Latitude
	Project string
	SSHKeys []string
	// AWS
	Group, UserData string
	Disk            int
}

// errRefused is a provider's refusal to create (no capacity, a spot request it will not fulfil): named, never retried
// another way.
var errRefused = errors.New("refused by the provider")

// provider is the little rent and return need of a provider; unit tests use a fake.
type provider interface {
	// find lists the boxes named name that still exist, and the live spot requests tagged with it.
	find(name string) ([]box, []request, error)
	create(s rentSpec) (box, error)
	get(id string) (box, error)
	remove(id string) error
	cancel(id string) error
	requestState(id string) (string, error)
	stop(id string) error
	// price is dollars an hour read from the provider for what spec rents, 0 when the provider does not say.
	price(s rentSpec) (float64, error)
}

// ---- Latitude.sh, over its HTTP API with LATITUDE_API_KEY ----

var latBase = "https://api.latitude.sh"

type latitude struct{ key string }

func (l latitude) call(method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body) // ignored: maps of strings always marshal
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, latBase+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+l.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("latitude %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body) // ignored: a short body is judged by its status and decode below
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return resp.StatusCode, b, fmt.Errorf("latitude %s %s: HTTP %d: LATITUDE_API_KEY was refused; make a new token in the Latitude dashboard > API keys, then: nova-secrets set LATITUDE_API_KEY", method, path, resp.StatusCode)
	}
	return resp.StatusCode, b, nil
}

type latServer struct {
	ID         string `json:"id"`
	Attributes struct {
		Hostname   string  `json:"hostname"`
		Status     string  `json:"status"`
		IPv4       string  `json:"primary_ipv4"`
		Price      float64 `json:"price"`
		Interfaces []struct {
			Name string `json:"name"`
			MAC  string `json:"mac_address"`
		} `json:"interfaces"`
	} `json:"attributes"`
}

func (s latServer) box() box {
	b := box{ID: s.ID, Name: s.Attributes.Hostname, Address: s.Attributes.IPv4, Price: s.Attributes.Price, State: "deploying"}
	if s.Attributes.Status == "on" {
		b.State = "running"
	} else if s.Attributes.Status == "off" {
		b.State = "stopped"
	}
	for _, i := range s.Attributes.Interfaces {
		if i.Name == "ETH0" {
			b.MAC = strings.ToLower(i.MAC)
		}
	}
	return b
}

func (l latitude) find(name string) ([]box, []request, error) {
	var out []box
	for page := 1; ; page++ {
		c, b, err := l.call("GET", fmt.Sprintf("/servers?page[size]=100&page[number]=%d", page), nil)
		if err == nil && c != 200 {
			err = fmt.Errorf("latitude GET /servers: HTTP %d: %s", c, oneLine(string(b)))
		}
		if err != nil {
			return nil, nil, err
		}
		var v struct{ Data []latServer }
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, nil, fmt.Errorf("latitude GET /servers: not JSON (%w)", err)
		}
		for _, s := range v.Data {
			if s.Attributes.Hostname == name {
				out = append(out, s.box())
			}
		}
		if len(v.Data) < 100 {
			return out, nil, nil
		}
	}
}

func (l latitude) get(id string) (box, error) {
	c, b, err := l.call("GET", "/servers/"+id, nil)
	switch {
	case err != nil:
		return box{}, err
	case c == 404:
		return box{ID: id, State: "gone"}, nil
	case c != 200:
		return box{}, fmt.Errorf("latitude GET /servers/%s: HTTP %d: %s", id, c, oneLine(string(b)))
	}
	var v struct{ Data latServer }
	if err := json.Unmarshal(b, &v); err != nil {
		return box{}, fmt.Errorf("latitude GET /servers/%s: not JSON (%w)", id, err)
	}
	return v.Data.box(), nil
}

func (l latitude) create(s rentSpec) (box, error) {
	if s.Spot {
		return box{}, fmt.Errorf("latitude has no spot market")
	}
	c, b, err := l.call("POST", "/servers", map[string]any{"data": map[string]any{"type": "servers", "attributes": map[string]any{
		"project": s.Project, "plan": s.Plan, "site": s.Site, "operating_system": s.Image, "hostname": s.Name,
		"ssh_keys": s.SSHKeys, "billing": "hourly"}}})
	switch {
	case err != nil:
		return box{}, err
	case c == 402 || c == 422 && strings.Contains(strings.ToLower(string(b)), "stock"):
		return box{}, fmt.Errorf("%w: latitude POST /servers: HTTP %d: %s", errRefused, c, oneLine(string(b)))
	case c != 200 && c != 201:
		return box{}, fmt.Errorf("latitude POST /servers: HTTP %d: %s", c, oneLine(string(b)))
	}
	var v struct{ Data latServer }
	if err := json.Unmarshal(b, &v); err != nil || v.Data.ID == "" {
		return box{}, fmt.Errorf("latitude POST /servers: no server id in the answer (%v)", err)
	}
	return v.Data.box(), nil
}

func (l latitude) remove(id string) error {
	c, b, err := l.call("DELETE", "/servers/"+id, nil)
	if err == nil && c != 200 && c != 204 && c != 404 {
		err = fmt.Errorf("latitude DELETE /servers/%s: HTTP %d: %s", id, c, oneLine(string(b)))
	}
	return err
}

func (latitude) cancel(string) error { return nil }

func (latitude) requestState(string) (string, error) { return "", nil }

func (latitude) stop(string) error {
	return fmt.Errorf("a Latitude box bills until it is deleted, so it is never stopped; run: nova-fleet return <name>")
}

func (latitude) price(rentSpec) (float64, error) { return 0, nil } // read off the box once it exists

// ---- AWS EC2, through the aws CLI under nova-secrets exec ----

// awsCLI runs the aws CLI with the given arguments and returns its stdout; the caller supplies how.
type awsCLI func(args ...string) (string, error)

type aws struct{ cli awsCLI }

type awsInstance struct {
	InstanceID            string `json:"InstanceId"`
	State                 struct{ Name string }
	PublicIPAddress       string `json:"PublicIpAddress"`
	SpotInstanceRequestID string `json:"SpotInstanceRequestId"`
	Tags                  []struct{ Key, Value string }
}

func (i awsInstance) box() box {
	b := box{ID: i.InstanceID, State: i.State.Name, Address: i.PublicIPAddress, Request: i.SpotInstanceRequestID}
	if b.State == "terminated" {
		b.State = "gone"
	}
	for _, t := range i.Tags {
		if t.Key == "Name" {
			b.Name = t.Value
		}
	}
	return b
}

func instances(out string) ([]awsInstance, error) {
	var v struct {
		Reservations []struct{ Instances []awsInstance }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("aws ec2 describe-instances: not JSON (%w)", err)
	}
	var all []awsInstance
	for _, r := range v.Reservations {
		all = append(all, r.Instances...)
	}
	return all, nil
}

func (a aws) find(name string) ([]box, []request, error) {
	out, err := a.cli("ec2", "describe-instances", "--output", "json", "--filters", "Name=tag:Name,Values="+name,
		"Name=instance-state-name,Values=pending,running,shutting-down,stopping,stopped")
	if err != nil {
		return nil, nil, err
	}
	all, err := instances(out)
	if err != nil {
		return nil, nil, err
	}
	var boxes []box
	for _, i := range all {
		boxes = append(boxes, i.box())
	}
	out, err = a.cli("ec2", "describe-spot-instance-requests", "--output", "json", "--filters", "Name=tag:Name,Values="+name, "Name=state,Values=open,active")
	if err != nil {
		return nil, nil, err
	}
	var v struct {
		SpotInstanceRequests []struct {
			SpotInstanceRequestID string `json:"SpotInstanceRequestId"`
			State                 string
			InstanceID            string `json:"InstanceId"`
		}
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, nil, fmt.Errorf("aws ec2 describe-spot-instance-requests: not JSON (%w)", err)
	}
	var reqs []request
	for _, r := range v.SpotInstanceRequests {
		reqs = append(reqs, request{ID: r.SpotInstanceRequestID, State: r.State, Box: r.InstanceID})
	}
	return boxes, reqs, nil
}

func (a aws) get(id string) (box, error) {
	out, err := a.cli("ec2", "describe-instances", "--output", "json", "--instance-ids", id)
	if err != nil {
		if strings.Contains(err.Error(), "InvalidInstanceID.NotFound") {
			return box{ID: id, State: "gone"}, nil
		}
		return box{}, err
	}
	all, err := instances(out)
	if err != nil || len(all) == 0 {
		return box{ID: id, State: "gone"}, err
	}
	return all[0].box(), nil
}

// create launches one instance tagged Name=<name> (instance, volume and spot request). --spot is a one-time request
// whose interruption terminates the box: a box AWS takes back stays gone, and stops billing (a persistent request,
// interruption stop, restarted one three times to bill idle, 2026-11-12).
func (a aws) create(s rentSpec) (box, error) {
	root, err := a.cli("ec2", "describe-images", "--output", "text", "--image-ids", s.Image, "--query", "Images[0].RootDeviceName")
	if root = strings.TrimSpace(root); err != nil || !strings.HasPrefix(root, "/dev/") {
		return box{}, fmt.Errorf("aws ec2 describe-images %s: no root device name (%v)", s.Image, err)
	}
	tag := "{Key=Name,Value=" + s.Name + "}"
	specs := "ResourceType=instance,Tags=[" + tag + "] ResourceType=volume,Tags=[" + tag + "]"
	args := []string{"ec2", "run-instances", "--output", "json", "--count", "1", "--image-id", s.Image, "--instance-type", s.Plan,
		"--security-group-ids", s.Group, "--placement", "AvailabilityZone=" + s.Site, "--metadata-options", "HttpTokens=required,HttpEndpoint=enabled",
		"--block-device-mappings", fmt.Sprintf("DeviceName=%s,Ebs={VolumeSize=%d,VolumeType=gp3,DeleteOnTermination=true}", root, s.Disk)}
	if s.UserData != "" {
		args = append(args, "--user-data", "file://"+s.UserData)
	}
	if s.Spot {
		args = append(args, "--instance-market-options", "MarketType=spot,SpotOptions={SpotInstanceType=one-time,InstanceInterruptionBehavior=terminate}")
		specs += " ResourceType=spot-instances-request,Tags=[" + tag + "]"
	}
	args = append(args, "--tag-specifications")
	args = append(args, strings.Fields(specs)...)
	out, err := a.cli(args...)
	if err != nil {
		for _, code := range []string{"InsufficientInstanceCapacity", "SpotMaxPriceTooLow", "MaxSpotInstanceCountExceeded", "InsufficientCapacity", "Unsupported"} {
			if strings.Contains(err.Error(), code) {
				return box{}, fmt.Errorf("%w: %v", errRefused, err)
			}
		}
		return box{}, err
	}
	var v struct{ Instances []awsInstance }
	if err := json.Unmarshal([]byte(out), &v); err != nil || len(v.Instances) != 1 {
		return box{}, fmt.Errorf("aws ec2 run-instances: want one instance in the answer (%v)", err)
	}
	b := v.Instances[0].box()
	b.Name = s.Name
	return b, nil
}

func (a aws) remove(id string) error {
	_, err := a.cli("ec2", "terminate-instances", "--output", "json", "--instance-ids", id)
	if err != nil && strings.Contains(err.Error(), "InvalidInstanceID.NotFound") {
		return nil
	}
	return err
}

func (a aws) cancel(id string) error {
	_, err := a.cli("ec2", "cancel-spot-instance-requests", "--output", "json", "--spot-instance-request-ids", id)
	if err != nil && strings.Contains(err.Error(), "InvalidSpotInstanceRequestID.NotFound") {
		return nil
	}
	return err
}

// requestState is the spot request's state, "gone" when AWS no longer knows it.
func (a aws) requestState(id string) (string, error) {
	out, err := a.cli("ec2", "describe-spot-instance-requests", "--output", "json", "--spot-instance-request-ids", id)
	if err != nil {
		if strings.Contains(err.Error(), "InvalidSpotInstanceRequestID.NotFound") {
			return "gone", nil
		}
		return "", err
	}
	var v struct{ SpotInstanceRequests []struct{ State string } }
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return "", fmt.Errorf("aws ec2 describe-spot-instance-requests: not JSON (%w)", err)
	}
	if len(v.SpotInstanceRequests) == 0 {
		return "gone", nil
	}
	return v.SpotInstanceRequests[0].State, nil
}

func (a aws) stop(id string) error {
	_, err := a.cli("ec2", "stop-instances", "--output", "json", "--instance-ids", id)
	return err
}

// price is the spot price now for the plan in the site (the fleet's IAM user cannot read the on-demand price list,
// so on-demand is 0: unknown unless --price gives it).
func (a aws) price(s rentSpec) (float64, error) {
	if !s.Spot {
		return 0, nil
	}
	platform, err := a.cli("ec2", "describe-images", "--output", "text", "--image-ids", s.Image, "--query", "Images[0].PlatformDetails")
	if err != nil {
		return 0, err
	}
	product := map[bool]string{true: "Windows", false: "Linux/UNIX"}[strings.HasPrefix(strings.TrimSpace(platform), "Windows")]
	out, err := a.cli("ec2", "describe-spot-price-history", "--output", "json", "--instance-types", s.Plan, "--availability-zone", s.Site,
		"--product-descriptions", product, "--start-time", now().UTC().Format(time.RFC3339), "--max-items", "1")
	if err != nil {
		return 0, err
	}
	var v struct{ SpotPriceHistory []struct{ SpotPrice string } }
	if err := json.Unmarshal([]byte(out), &v); err != nil || len(v.SpotPriceHistory) == 0 {
		return 0, fmt.Errorf("aws ec2 describe-spot-price-history: no price for %s in %s (%v)", s.Plan, s.Site, err)
	}
	return strconv.ParseFloat(v.SpotPriceHistory[0].SpotPrice, 64)
}

// awsThrough runs the aws CLI under nova-secrets exec with only the fleet's AWS keys, region us-east-1 unless set.
func awsThrough(secrets, cli string) awsCLI {
	return func(args ...string) (string, error) {
		env := append(os.Environ(), "AWS_PAGER=")
		if os.Getenv("AWS_DEFAULT_REGION") == "" {
			env = append(env, "AWS_DEFAULT_REGION=us-east-1")
		}
		out, why, c := outCmd(secrets, append([]string{"exec", "--only", "AWS_ACCESS_KEY_ID,AWS_SECRET_ACCESS_KEY", "--", cli}, args...), env)
		if c != 0 {
			return out, fmt.Errorf("aws %s exit %d: %s", strings.Join(args[:min(2, len(args))], " "), c, oneLine(why))
		}
		return out, nil
	}
}

// nicHint is the NIC an ETH0 MAC prefix has meant on Latitude (nic-prefixes.tsv): the PCI id, or "" when unknown.
func nicHint(mac string) (pci, what string) {
	b, _ := embedded.ReadFile("nic-prefixes.tsv") // ignored: embedded at build time
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 3 && !strings.HasPrefix(line, "#") && f[0] != "prefix" && strings.HasPrefix(strings.ToLower(mac), f[0]) {
			return f[1], f[2]
		}
	}
	return "", ""
}
