package moodle

import (
	"fmt"
	"testing"
)

type PrintMoodleLogger struct {
}

func (ml *PrintMoodleLogger) Debug(message string, items ...interface{}) error {
	fmt.Println(message, items)
	return nil
}

func TestAssignmentGrades(t *testing.T) {

	api := NewMoodleApi(requireEnv("MOODLE_URL", t), requireEnv("MOODLE_KEY", t))
	api.SetLogger(&PrintMoodleLogger{})

	r, err := api.GetAssignmentGrades(91)
	if err != nil {
		t.Errorf("API call failed")
		return
	}
	if r == nil {
		t.Errorf("API call should have returned a result")
		return
	}
	if len(*r) < 1 {
		t.Errorf("No results found")
		return
	}

	fmt.Printf("%v\n", *r)
}

func TestGetAssignments(t *testing.T) {

	api := NewMoodleApi(requireEnv("MOODLE_URL", t), requireEnv("MOODLE_KEY", t))
	api.SetLogger(&PrintMoodleLogger{})

	r, err := api.GetAssignments(&[]Course{Course{MoodleId: 47}})
	if err != nil {
		t.Errorf("API call failed")
		return
	}
	if r == nil {
		t.Errorf("API call should have returned a result")
		return
	}
	if len(*r) < 1 {
		t.Errorf("No results found")
		return
	}

	for _, a := range *r {
		fmt.Printf("%v\n", a)
	}
}
