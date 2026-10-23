package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/github-radar/github-radar/internal/auth"
	"github.com/github-radar/github-radar/internal/config"
	"github.com/github-radar/github-radar/internal/db"
	"golang.org/x/term"
)

type passwordReader func(fd int) ([]byte, error)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, term.ReadPassword); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, input io.Reader, output io.Writer, readPassword passwordReader) error {
	if len(args) >= 2 && args[0] == "admin" && args[1] == "create" {
		if len(args) != 2 {
			return fmt.Errorf("usage: radar admin create")
		}
		return runAdminCreate(ctx, input, output, readPassword)
	}
	return fmt.Errorf("usage: radar admin create")
}

func runAdminCreate(ctx context.Context, input io.Reader, output io.Writer, readPassword passwordReader) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	store, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer store.Close()

	reader := bufio.NewReader(input)
	fmt.Fprint(output, "Username: ")
	username, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read username: %w", err)
	}
	fmt.Fprint(output, "Password: ")
	password, err := readPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(output)
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	fmt.Fprint(output, "Confirm password: ")
	confirmation, err := readPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(output)
	if err != nil {
		return fmt.Errorf("read password confirmation: %w", err)
	}
	if string(password) != string(confirmation) {
		return fmt.Errorf("passwords do not match")
	}
	admin, err := auth.NewService(store.SQL()).CreateAdmin(ctx, strings.TrimSpace(username), string(password))
	if err != nil {
		if errors.Is(err, auth.ErrAdminExists) {
			return fmt.Errorf("administrator already exists")
		}
		return err
	}
	fmt.Fprintf(output, "Administrator %s created.\n", admin.Username)
	return nil
}
