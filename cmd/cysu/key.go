package main

// key.go restores local key initialization. The derived key is saved in the
// private config file and mirrored to master.key for existing worker images.

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

var keyResource = resource{
	name: "key", title: "Key (encryption)",
	commands: []command{{
		name: "init",
		flags: []flagSpec{{
			name: "passphrase", placeholder: "passphrase",
			usage: "passphrase to derive and save the key (overrides saved setup and CYSU_PASSPHRASE)",
		}},
		run: initKey,
	}},
}

// initKey derives a master key, saves it in config, and writes the legacy
// file expected by published worker images. A new passphrase replaces the key.
func initKey(v flagValues) error {
	cfg, err := readConfig()
	if err != nil {
		return err
	}
	passphrase := cfg.Passphrase
	if v.provided("passphrase") {
		passphrase = v["passphrase"]
	} else if passphrase == "" {
		passphrase = os.Getenv("CYSU_PASSPHRASE")
	}
	if passphrase == "" && !v.provided("passphrase") {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return cliError{message: "missing passphrase: run cysu setup, pass --passphrase, or set CYSU_PASSPHRASE", exit: exitUsage}
		}
		fmt.Fprint(os.Stderr, "Encryption passphrase: ")
		raw, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading passphrase: %w", err)
		}
		passphrase = strings.TrimRight(raw, "\r\n")
	}
	if passphrase == "" {
		return cliError{message: "passphrase must not be empty", exit: exitUsage}
	}
	pass := []byte(passphrase)
	key := deriveMasterKey(pass)
	zero(pass)
	cfg.Passphrase = passphrase
	cfg.MasterKey = key
	path, err := writeConfig(cfg)
	if err != nil {
		zero(key)
		return err
	}
	if err := writeLegacyKeyFile(key); err != nil {
		zero(key)
		return err
	}
	zero(cachedKey)
	cachedKey = key
	fmt.Println(path)
	return nil
}
