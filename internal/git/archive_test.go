package git

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// archiveStub routes git subcommands to canned responses so the archive
// lifecycle can be exercised without a real repo. It keys on the first
// meaningful argument.
type archiveStub struct {
	revParse   func(args []string) (string, string, error) // rev-parse
	updateRef  func(args []string) (string, string, error) // update-ref (create/delete)
	stashDrop  func(args []string) (string, string, error) // stash drop
	stashStore func(args []string) (string, string, error) // stash store
	forEachRef func(args []string) (string, string, error) // for-each-ref
	log        func(args []string) (string, string, error) // log
	calls      []string
}

func installArchiveStub(t *testing.T, s *archiveStub) {
	t.Helper()
	orig := runner
	runner = func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		s.calls = append(s.calls, strings.Join(args, " "))
		out, errOut, err := s.route(args)
		return []byte(out), []byte(errOut), err
	}
	t.Cleanup(func() { runner = orig })
}

func (s *archiveStub) route(args []string) (string, string, error) {
	if len(args) == 0 {
		return "", "", nil
	}
	switch args[0] {
	case "rev-parse":
		if s.revParse != nil {
			return s.revParse(args)
		}
	case "update-ref":
		if s.updateRef != nil {
			return s.updateRef(args)
		}
	case "for-each-ref":
		if s.forEachRef != nil {
			return s.forEachRef(args)
		}
	case "log":
		if s.log != nil {
			return s.log(args)
		}
	case "stash":
		if len(args) > 1 && args[1] == "drop" && s.stashDrop != nil {
			return s.stashDrop(args)
		}
		if len(args) > 1 && args[1] == "store" && s.stashStore != nil {
			return s.stashStore(args)
		}
	}
	return "", "", nil
}

func TestArchivePinsThenDrops(t *testing.T) {
	const sha = "abc123def4567890abc123def4567890abc123de"
	var pinned, dropped bool
	installArchiveStub(t, &archiveStub{
		revParse: func(args []string) (string, string, error) {
			// resolveStashSHA: rev-parse stash@{1}; refExists: rev-parse --verify --quiet <ref>
			if contains(args, "--verify") {
				return "", "", errors.New("exit 1") // not yet archived
			}
			return sha + "\n", "", nil
		},
		updateRef: func(args []string) (string, string, error) {
			pinned = true
			if !contains(args, archiveRefPrefix+sha) {
				t.Errorf("update-ref args missing archive ref: %v", args)
			}
			return "", "", nil
		},
		stashDrop: func(args []string) (string, string, error) {
			dropped = true
			if !pinned {
				t.Error("drop happened before pin — unsafe ordering")
			}
			return "", "", nil
		},
	})

	got, err := Archive(context.Background(), "stash@{1}")
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if got != sha {
		t.Errorf("Archive() sha = %q, want %q", got, sha)
	}
	if !pinned || !dropped {
		t.Errorf("expected pin(%v) and drop(%v)", pinned, dropped)
	}
}

func TestArchiveAlreadyArchived(t *testing.T) {
	const sha = "abc123def4567890abc123def4567890abc123de"
	installArchiveStub(t, &archiveStub{
		revParse: func(args []string) (string, string, error) {
			if contains(args, "--verify") {
				return sha + "\n", "", nil // ref exists
			}
			return sha + "\n", "", nil
		},
		updateRef: func(args []string) (string, string, error) {
			t.Error("update-ref should not run when already archived")
			return "", "", nil
		},
	})
	_, err := Archive(context.Background(), "stash@{0}")
	if !errors.Is(err, ErrAlreadyArchived) {
		t.Fatalf("Archive() error = %v, want ErrAlreadyArchived", err)
	}
}

func TestArchiveDropFailureKeepsPin(t *testing.T) {
	const sha = "abc123def4567890abc123def4567890abc123de"
	var pinned bool
	installArchiveStub(t, &archiveStub{
		revParse: func(args []string) (string, string, error) {
			if contains(args, "--verify") {
				return "", "", errors.New("exit 1")
			}
			return sha + "\n", "", nil
		},
		updateRef: func(args []string) (string, string, error) { pinned = true; return "", "", nil },
		stashDrop: func(args []string) (string, string, error) {
			return "", "fatal: cannot drop", errors.New("exit 1")
		},
	})
	_, err := Archive(context.Background(), "stash@{0}")
	if err == nil {
		t.Fatal("Archive() = nil, want the drop error surfaced")
	}
	if !pinned {
		t.Error("expected the commit to remain pinned after a failed drop")
	}
	if !strings.Contains(err.Error(), "pinned") {
		t.Errorf("error should note the commit is still pinned, got %q", err)
	}
}

func TestArchivedStashesParsesAndSorts(t *testing.T) {
	older := time.Now().Add(-72 * time.Hour).Unix()
	newer := time.Now().Add(-1 * time.Hour).Unix()
	sha1 := "1111111111111111111111111111111111111111"
	sha2 := "2222222222222222222222222222222222222222"
	installArchiveStub(t, &archiveStub{
		forEachRef: func(args []string) (string, string, error) {
			// objectname<sep>archived-unix
			out := sha1 + fieldSep + itoaTest(older) + "\n" +
				sha2 + fieldSep + itoaTest(newer) + "\n"
			return out, "", nil
		},
		log: func(args []string) (string, string, error) {
			out := sha1 + fieldSep + itoaTest(older) + fieldSep + "WIP on main: old work\n" +
				sha2 + fieldSep + itoaTest(newer) + fieldSep + "On feature/x: new work\n"
			return out, "", nil
		},
	})
	got, err := ArchivedStashes(context.Background())
	if err != nil {
		t.Fatalf("ArchivedStashes() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d archived, want 2", len(got))
	}
	// Newest archived first.
	if got[0].SHA != sha2 {
		t.Errorf("expected newest-archived first, got %q", got[0].SHA)
	}
	if got[0].Branch != "feature/x" {
		t.Errorf("branch = %q, want feature/x", got[0].Branch)
	}
}

func TestArchivedStashesEmpty(t *testing.T) {
	installArchiveStub(t, &archiveStub{
		forEachRef: func(args []string) (string, string, error) { return "", "", nil },
	})
	got, err := ArchivedStashes(context.Background())
	if err != nil {
		t.Fatalf("ArchivedStashes() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}

func TestRestoreStoresThenDeletesRef(t *testing.T) {
	const sha = "abcdef1234567890abcdef1234567890abcdef12"
	var stored, deleted bool
	installArchiveStub(t, &archiveStub{
		forEachRef: func(args []string) (string, string, error) {
			return sha + fieldSep + "0\n", "", nil
		},
		log: func(args []string) (string, string, error) {
			return sha + fieldSep + "0" + fieldSep + "WIP on main: revived\n", "", nil
		},
		stashStore: func(args []string) (string, string, error) {
			stored = true
			return "", "", nil
		},
		updateRef: func(args []string) (string, string, error) {
			if contains(args, "-d") {
				deleted = true
				if !stored {
					t.Error("archive ref deleted before the stash was re-stored")
				}
			}
			return "", "", nil
		},
	})
	got, err := Restore(context.Background(), sha[:8])
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got != sha {
		t.Errorf("Restore() sha = %q, want %q", got, sha)
	}
	if !stored || !deleted {
		t.Errorf("expected store(%v) and delete(%v)", stored, deleted)
	}
}

func TestRestoreUnknownID(t *testing.T) {
	installArchiveStub(t, &archiveStub{
		forEachRef: func(args []string) (string, string, error) { return "", "", nil },
	})
	_, err := Restore(context.Background(), "deadbeef")
	if !errors.Is(err, ErrNotArchived) {
		t.Fatalf("Restore() error = %v, want ErrNotArchived", err)
	}
}

func TestResolveArchivedSHAAmbiguous(t *testing.T) {
	sha1 := "abc1000000000000000000000000000000000000"
	sha2 := "abc2000000000000000000000000000000000000"
	installArchiveStub(t, &archiveStub{
		forEachRef: func(args []string) (string, string, error) {
			return sha1 + fieldSep + "0\n" + sha2 + fieldSep + "0\n", "", nil
		},
		log: func(args []string) (string, string, error) {
			return sha1 + fieldSep + "0" + fieldSep + "On main: a\n" +
				sha2 + fieldSep + "0" + fieldSep + "On main: b\n", "", nil
		},
	})
	_, err := resolveArchivedSHA(context.Background(), "abc")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("resolveArchivedSHA() error = %v, want ambiguous", err)
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func itoaTest(n int64) string {
	return itoa64(n)
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
