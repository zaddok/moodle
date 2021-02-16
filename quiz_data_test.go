package moodle

import (
	"fmt"
	"testing"
)

func TestGetQuizzesWithCourseId(t *testing.T) {

	api := NewMoodleApi(requireEnv("MOODLE_URL", t), requireEnv("MOODLE_KEY", t))
	api.SetLogger(&PrintMoodleLogger{})

	fmt.Println("Check for Quizzes")
	quizzes, err := api.GetQuizzesWithCourseId([]int{36})
	if err != nil {
		t.Errorf("API call failed: %s", err)
		return
	}
	if len(quizzes) < 1 {
		fmt.Println("No quizzes found")
		return
	}
	for _, a := range quizzes {
		fmt.Printf("id: %d, course: %d, %v,%v, grademethod: %v, grade: %v, behaviour: %v\n", a.Id, a.CourseId, a.Name, a.TimeClose, a.GradeMethod, a.Grade, a.PreferredBehaviour)

		/*

			discussions, err := api.GetQuizResults(int(a.Id))
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
		*/

	}
	fmt.Println()

}
