package moodle

import (
	"fmt"
	"testing"
)

func TestGetForumsWithCourseId(t *testing.T) {

	api := NewMoodleApi(requireEnv("MOODLE_URL", t), requireEnv("MOODLE_KEY", t))
	api.SetLogger(&PrintMoodleLogger{})

	fmt.Println("Check for Forums")
	forums, err := api.GetForumsWithCourseId([]int{194})
	if err != nil {
		t.Errorf("API call failed: %s", err)
		return
	}
	if len(forums) < 1 {
		t.Errorf("No results found")
		return
	}
	for _, a := range forums {
		fmt.Printf("%v,%v,%v, scale: %v, grade: %v, assessed: %v, type: %v\n", a.CourseId, a.Name, a.DueDate, a.Scale, a.Grade, a.Assessed, a.Type)

		discussions, err := api.GetForumsDiscussions(int(a.Id))
		if err != nil {
			t.Errorf("API call failed: %s", err)
			return
		}
		if len(discussions) < 1 {
			fmt.Println("    No discussions found")
		}
		for _, d := range discussions {
			fmt.Println("    ", d.Id, d.Name, d.Created)
		}
	}
	fmt.Println()

}
