package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type CommandOutcome struct {
	Name     string   `json:"name"`
	Args     []string `json:"args"`
	ExitCode int      `json:"exit_code"`
}
type Journal struct {
	Job       Job              `json:"job"`
	Candidate Candidate        `json:"candidate"`
	Phase     string           `json:"phase"`
	Outcomes  []CommandOutcome `json:"outcomes"`
	Pending   *Advance         `json:"pending,omitempty"`
}

func SaveJournal(root string, j Journal) error {
	if !jobID.MatchString(j.Job.ID) {
		return fmt.Errorf("invalid journal job ID")
	}
	if err := privateDir(root); err != nil {
		return err
	}
	dir := filepath.Join(root, j.Job.ID)
	if err := privateDir(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".journal-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(dir, "journal.json")); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func LoadJournal(root, id string) (Journal, error) {
	var j Journal
	if !jobID.MatchString(id) {
		return j, fmt.Errorf("invalid journal job ID")
	}
	path := filepath.Join(root, id, "journal.json")
	if err := privateFile(path); err != nil {
		return j, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return j, err
	}
	if err = json.Unmarshal(data, &j); err != nil {
		return Journal{}, err
	}
	if j.Job.ID != id {
		return Journal{}, fmt.Errorf("journal identity mismatch")
	}
	return j, nil
}
