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

	r, err := api.GetAssignmentGrades(3)
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

func TestGetAssessmentInformation(t *testing.T) {

	api := NewMoodleApi(requireEnv("MOODLE_URL", t), requireEnv("MOODLE_KEY", t))
	api.SetLogger(&PrintMoodleLogger{})

	r, err := api.GetAssignmentsWithCourseId([]int{3})
	if err != nil {
		t.Errorf("API call failed")
		return
	}
	if len(r) < 1 {
		t.Errorf("No results found")
		return
	}
	for _, a := range r {
		fmt.Printf("%v,%v,%v\n", a.CourseId, a.Name, a.DueDate)
	}

	s, err := api.GetQuizzesWithCourseId([]int{3})
	if err != nil {
		t.Errorf("API call failed: %s", err)
		return
	}
	if len(s) < 1 {
		t.Errorf("No results found")
		return
	}
	for _, a := range s {
		fmt.Printf("%v,%v,%v\n", a.CourseId, a.Name, a.TimeClose)
	}

	f, err := api.GetForumsWithCourseId([]int{3})
	if err != nil {
		t.Errorf("API call failed: %s", err)
		return
	}
	if len(f) < 1 {
		t.Errorf("No results found")
		return
	}
	for _, a := range f {
		fmt.Printf("%v,%v,%v,%v,%v\n", a.CourseId, a.Name, a.DueDate, a.Scale, a.Grade)
	}

	m, err := api.GetCourseGradebook(3)
	if err != nil {
		t.Errorf("API call failed: %s", err)
		return
	}
	if m == nil {
		t.Errorf("No results found")
		return
	}
	fmt.Println(m)

}
