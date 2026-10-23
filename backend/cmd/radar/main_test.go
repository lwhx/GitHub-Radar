package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestAdminCreatePromptsForPasswordAndPrintsOnlyUsername(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_PATH", t.TempDir()+"/radar.db")
	passwords := [][]byte{[]byte("correct horse battery staple"), []byte("correct horse battery staple")}
	passwordCalls := 0
	readPassword := func(int) ([]byte, error) {
		value := passwords[passwordCalls]
		passwordCalls++
		return value, nil
	}
	var output bytes.Buffer

	err := run(context.Background(), []string{"admin", "create"}, strings.NewReader("Radar.Admin\n"), &output, readPassword)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if passwordCalls != 2 {
		t.Errorf("password reader calls = %d, want 2", passwordCalls)
	}
	got := output.String()
	if !strings.Contains(got, "radar.admin") || strings.Contains(got, "correct horse") || strings.Contains(got, "$argon2id$") {
		t.Fatalf("output = %q", got)
	}
}

func TestAdminCreateRejectsExtraArgumentsWithoutReadingPassword(t *testing.T) {
	called := false
	err := run(context.Background(), []string{"admin", "create", "extra"}, strings.NewReader("radar\n"), io.Discard, func(int) ([]byte, error) {
		called = true
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("run() error = %v, want usage error", err)
	}
	if called {
		t.Fatal("password reader was called for invalid arguments")
	}
}

func TestAdminCreateRejectsPasswordMismatchAndDuplicate(t *testing.T) {
	for _, test := range []struct {
		name      string
		passwords [][]byte
		prepare   bool
		want      string
	}{
		{name: "mismatch", passwords: [][]byte{[]byte("correct horse battery staple"), []byte("different secure password")}, want: "do not match"},
		{name: "duplicate", passwords: [][]byte{[]byte("correct horse battery staple"), []byte("correct horse battery staple")}, prepare: true, want: "already exists"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APP_ENV", "development")
			t.Setenv("DATABASE_PATH", t.TempDir()+"/radar.db")
			if test.prepare {
				values := [][]byte{[]byte("correct horse battery staple"), []byte("correct horse battery staple")}
				i := 0
				if err := run(context.Background(), []string{"admin", "create"}, strings.NewReader("radar\n"), io.Discard, func(int) ([]byte, error) {
					value := values[i]
					i++
					return value, nil
				}); err != nil {
					t.Fatalf("prepare run() error = %v", err)
				}
			}
			i := 0
			err := run(context.Background(), []string{"admin", "create"}, strings.NewReader("radar\n"), io.Discard, func(int) ([]byte, error) {
				value := test.passwords[i]
				i++
				return value, nil
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want text %q", err, test.want)
			}
		})
	}
}
