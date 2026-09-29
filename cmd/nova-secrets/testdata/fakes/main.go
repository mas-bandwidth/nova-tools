package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

func main() {
	prog := filepath.Base(os.Args[0])
	if strings.HasPrefix(prog, "age-keygen") {
		runAgeKeygen(os.Args[1:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "age-keygen" {
		runAgeKeygen(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "sops" {
		runSops(os.Args[2:])
		return
	}
	runSops(os.Args[1:])
}

func runAgeKeygen(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "age-keygen: no arguments")
		os.Exit(1)
	}
	for _, a := range args {
		if a == "--version" || a == "-version" {
			fmt.Println("v1.3.2")
			return
		}
	}
	for i, a := range args {
		if a == "-y" && i+1 < len(args) {
			path := args[i+1]
			data, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "age-keygen: %v\n", err)
				os.Exit(1)
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "# public key: ") {
					fmt.Println(strings.TrimSpace(strings.TrimPrefix(line, "# public key: ")))
					return
				}
				if strings.HasPrefix(line, "AGE-SECRET-KEY-1") {
					rest := strings.TrimPrefix(line, "AGE-SECRET-KEY-1")
					fmt.Println("age1" + strings.ToLower(rest))
					return
				}
			}
			fmt.Fprintln(os.Stderr, "age-keygen: public key comment missing")
			os.Exit(1)
		}
	}

	outPath := ""
	for i, a := range args {
		if a == "-o" && i+1 < len(args) {
			outPath = args[i+1]
			break
		}
	}
	if outPath == "" {
		fmt.Fprintln(os.Stderr, "age-keygen: missing -o")
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "age-keygen: %v\n", err)
		os.Exit(1)
	}

	pub, priv := generateAgeKeyPair(outPath)
	content := fmt.Sprintf("# created: %s\n# public key: %s\n%s\n",
		time.Now().UTC().Format(time.RFC3339), pub, priv)
	if err := os.WriteFile(outPath, []byte(content), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "age-keygen: %v\n", err)
		os.Exit(1)
	}
}

func generateAgeKeyPair(seed string) (pubKey, privKey string) {
	const bech32Alphabet = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	h := sha256.Sum256([]byte(seed + fmt.Sprintf("%d", time.Now().UnixNano())))
	var pub strings.Builder
	pub.WriteString("age1")
	for i := 0; i < 58; i++ {
		pub.WriteByte(bech32Alphabet[int(h[i%len(h)])%len(bech32Alphabet)])
	}
	pubKey = pub.String()
	privKey = "AGE-SECRET-KEY-1" + strings.ToUpper(pubKey[4:])
	return pubKey, privKey
}

func runSops(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sops: no arguments")
		os.Exit(1)
	}

	if args[0] == "--version" {
		fmt.Println("sops 3.13.3")
		return
	}

	if args[0] == "-d" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sops: no file specified")
			os.Exit(100)
		}
		filePath := args[1]
		data, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sops: cannot open %s\n", filePath)
			os.Exit(1)
		}
		content := string(data)
		if !strings.Contains(content, "sops:") {
			fmt.Fprintf(os.Stderr, "sops metadata not found in %s\n", filePath)
			os.Exit(2)
		}

		keyFile := os.Getenv("SOPS_AGE_KEY_FILE")
		if keyFile == "" {
			fmt.Fprintln(os.Stderr, "no identity file")
			os.Exit(128)
		}
		keyData, err := os.ReadFile(keyFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "no identity file")
			os.Exit(128)
		}

		pubKey := ""
		for _, line := range strings.Split(string(keyData), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "# public key: ") {
				pubKey = strings.TrimSpace(strings.TrimPrefix(trimmed, "# public key: "))
				break
			}
			if strings.HasPrefix(trimmed, "AGE-SECRET-KEY-1") {
				rest := strings.TrimPrefix(trimmed, "AGE-SECRET-KEY-1")
				pubKey = "age1" + strings.ToLower(rest)
			}
		}
		if pubKey == "" {
			fmt.Fprintln(os.Stderr, "no identity matched any of the recipients")
			os.Exit(128)
		}

		recRegex := regexp.MustCompile(`recipient:\s*([a-z0-9]+)`)
		matches := recRegex.FindAllStringSubmatch(content, -1)
		found := false
		for _, m := range matches {
			if len(m) > 1 && m[1] == pubKey {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintln(os.Stderr, "no identity matched any of the recipients")
			os.Exit(128)
		}

		payloadRegex := regexp.MustCompile(`fake_payload:\s*([A-Za-z0-9+/=]+)`)
		if m := payloadRegex.FindStringSubmatch(content); len(m) > 1 {
			decoded, err := base64.StdEncoding.DecodeString(m[1])
			if err == nil {
				os.Stdout.Write(normalizeDecryptedYAML(decoded))
				return
			}
		}

		if idx := strings.Index(content, "PLAINTEXT\n"); idx >= 0 {
			os.Stdout.WriteString(content[idx+len("PLAINTEXT\n"):])
			return
		}

		var lines []string
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, "sops:") {
				break
			}
			lines = append(lines, line)
		}
		os.Stdout.WriteString(strings.Join(lines, "\n"))
		return
	}

	isEncrypt := false
	for _, a := range args {
		if a == "-e" {
			isEncrypt = true
			break
		}
	}
	if isEncrypt {
		var ageKeys []string
		filenameOverride := ""
		unencryptedRegex := ""
		var inputBytes []byte

		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--age":
				if i+1 < len(args) {
					ageKeys = strings.Split(args[i+1], ",")
					i++
				}
			case "--filename-override":
				if i+1 < len(args) {
					filenameOverride = args[i+1]
					i++
				}
			case "--unencrypted-regex":
				if i+1 < len(args) {
					unencryptedRegex = args[i+1]
					i++
				}
			}
		}

		lastArg := args[len(args)-1]
		if lastArg == "/dev/stdin" || lastArg == "-" {
			raw, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sops: stdin read error: %v\n", err)
				os.Exit(1)
			}
			inputBytes = raw
		} else if fi, err := os.Stat(lastArg); err == nil && !fi.IsDir() {
			raw, err := os.ReadFile(lastArg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sops: read error: %v\n", err)
				os.Exit(1)
			}
			inputBytes = raw
		} else {
			fmt.Fprintln(os.Stderr, "error: no file specified")
			os.Exit(100)
		}

		if filenameOverride != "" {
			cwd, _ := os.Getwd()
			cfg, err := secrets.ParseSopsConfig(cwd)
			if err != nil {
				fmt.Fprintln(os.Stderr, "config file not found")
				os.Exit(1)
			}
			rule, err := secrets.FindMatchingRule(cfg, filenameOverride)
			if err != nil || rule == nil {
				fmt.Fprintln(os.Stderr, "error: no matching creation rules found")
				os.Exit(1)
			}
			ageKeys = rule.Recipients
			if unencryptedRegex == "" {
				unencryptedRegex = rule.UnencryptedRegex
			}
		}

		if len(ageKeys) == 0 {
			targetDir := "."
			if lastArg != "/dev/stdin" && lastArg != "-" {
				targetDir = filepath.Dir(lastArg)
			}
			if cfg, err := secrets.ParseSopsConfig(targetDir); err == nil {
				rule, err := secrets.FindMatchingRule(cfg, filepath.Base(lastArg))
				if err == nil && rule != nil {
					ageKeys = rule.Recipients
					if unencryptedRegex == "" {
						unencryptedRegex = rule.UnencryptedRegex
					}
				}
			}
		}

		if len(ageKeys) == 0 {
			fmt.Fprintln(os.Stderr, "error: no recipients specified")
			os.Exit(1)
		}

		var unencRe *regexp.Regexp
		if unencryptedRegex != "" {
			unencRe, _ = regexp.Compile(unencryptedRegex)
		}

		var out strings.Builder
		scanner := bufio.NewScanner(strings.NewReader(string(inputBytes)))
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || trimmed == "" {
				out.WriteString(line + "\n")
				continue
			}
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.Contains(line, ":") {
				parts := strings.SplitN(line, ":", 2)
				keyName := strings.TrimSpace(parts[0])
				cleanKey := strings.Trim(keyName, `"'`)
				if unencRe != nil && (unencRe.MatchString(keyName) || unencRe.MatchString(cleanKey)) {
					out.WriteString(line + "\n")
				} else {
					out.WriteString(parts[0] + ": ENC[AES256_GCM,data:fake]\n")
				}
				continue
			}
		}

		out.WriteString("sops:\n")
		out.WriteString("    age:\n")
		for _, r := range ageKeys {
			r = strings.TrimSpace(r)
			if r != "" {
				out.WriteString(fmt.Sprintf("        - recipient: %s\n", r))
			}
		}
		out.WriteString("    fake_payload: " + base64.StdEncoding.EncodeToString(inputBytes) + "\n")

		os.Stdout.WriteString(out.String())
		return
	}

	fmt.Fprintf(os.Stderr, "sops: unknown command: %v\n", args)
	os.Exit(1)
}

func normalizeDecryptedYAML(raw []byte) []byte {
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			val := strings.TrimSpace(parts[1])
			if len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'' {
				inner := val[1 : len(val)-1]
				if !strings.Contains(inner, "'") && !strings.Contains(inner, "\n") {
					out.WriteString(parts[0] + ": " + inner + "\n")
					continue
				}
			}
		}
		out.WriteString(line + "\n")
	}
	return []byte(out.String())
}
