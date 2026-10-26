package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJournal(t *testing.T) {
	root := canonicalTempDir(t)
	j := Journal{Job: Job{ID: testJobID, SourceSHA: baseSHA, TargetSHA: releaseSHA}, Phase: "preparing", Candidate: Candidate{JobID: testJobID, CandidateSHA: mergedSHA}}
	if err := SaveJournal(root, j); err != nil {
		t.Fatal(err)
	}
	got, err := LoadJournal(root, testJobID)
	if err != nil || got.Candidate.CandidateSHA != mergedSHA {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	for _, p := range []string{filepath.Join(root, testJobID), filepath.Join(root, testJobID, "journal.json")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("insecure permissions: %s %v", p, info.Mode())
		}
	}
	j.Phase = "awaiting_review"
	if err := SaveJournal(root, j); err != nil {
		t.Fatal(err)
	}
	got, err = LoadJournal(root, testJobID)
	if err != nil || got.Phase != "awaiting_review" {
		t.Fatalf("replace failed: %v", err)
	}
	if _, err := LoadJournal(root, "../escape"); err == nil {
		t.Fatal("accepted traversal")
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "22222222-2222-4222-8222-222222222222")); err != nil {
		t.Fatal(err)
	}
	j.Job.ID = "22222222-2222-4222-8222-222222222222"
	if err := SaveJournal(root, j); err == nil {
		t.Fatal("followed job directory symlink")
	}
	bytes, _ := os.ReadFile(filepath.Join(root, testJobID, "journal.json"))
	if strings.Contains(string(bytes), "test-secret") {
		t.Fatal("token leaked")
	}
}
