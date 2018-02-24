# Moodle API for Golang

Basic Golang API for interacting with Moodle.


	api := moodle.NewMoodleApi("https://moodle.example.com/moodle/", "a0092ba9a9f5b45cdd2f01d049595bfe91", l)
	courses, _ := api.GetCourses("History")
	if courses != nil {
		for _, i := range *courses {
			fmt.Printf("%s\n", i.Code)
		}
	}

This is a simple Moodle API that wraps the Moodle JSON Web Service API. It was created
in a hurry to meet the needs of an existing project and is used in production. It will
be improved and enhanced over time. No warranty is given or implied. Use at your own risk.
