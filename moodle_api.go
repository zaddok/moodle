package moodle

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"sort"
	"strings"
	"time"

	"git.tai.io/jacob/log"
)

// API Documentation
// https://docs.moodle.org/dev/Web_service_API_functions

type MoodleApi struct {
	base  string
	token string
	log   log.Log

	smtpUser      string
	smtpPassword  string
	smtpHost      string
	smtpPort      int
	smtpFromName  string
	smtpFromEmail string
}

func NewMoodleApi(base string, token string, l log.Log) *MoodleApi {
	return &MoodleApi{
		base:  base,
		token: token,
		log:   l,
	}
}

func (m *MoodleApi) SetSmtpSettings(host string, port int, user, password string, fromName, fromEmail string) {
	m.smtpUser = user
	m.smtpPassword = password
	m.smtpHost = host
	m.smtpPort = port
	m.smtpFromName = fromName
	m.smtpFromEmail = fromEmail
}

type Course struct {
	Code        string        `json:",omitempty"`
	Name        string        `json:",omitempty"`
	Summary     string        `json:",omitempty"`
	Assignments []*Assignment `json:",omitempty"`
	Roles       []*Role       `json:",omitempty"`
	MoodleId    int64         `json:"-"`
	Created     *time.Time    `json:"-"`
	Start       *time.Time    `json:",omitempty"`
	End         *time.Time    `json:",omitempty"`
}

type Person struct {
	MoodleId      int64      `json:",omitempty"`
	AlphacrucisId string     `json:",omitempty"`
	Username      string     `json:",omitempty"`
	Email         string     `json:",omitempty"`
	PersonalEmail string     `json:",omitempty"`
	FirstName     string     `json:",omitempty"`
	LastName      string     `json:",omitempty"`
	Created       *time.Time `json:",omitempty"`
	Roles         []*Role    `json:"role,omitempty"`
}

type Role struct {
	Person             *Person `json:",omitempty"`
	Course             *Course `json:",omitempty"`
	Role               *RoleInfo
	Enrolled           *time.Time
	GradeInfo          []GradeInfo `json:",omitempty"`
	GradeOverride      bool
	GradeOverrideValue float64
	GradeFinal         float64
}

type Submission struct {
	MoodleId  int64
	Person    Person
	Submitted *time.Time
	Extension *time.Time
}

type Assignment struct {
	MoodleId    int64        `json:",omitempty"`
	Name        string       `json:",omitempty"`
	Due         *time.Time   `json:",omitempty"`
	Weight      float64      `json:",omitempty"`
	Description string       `json:",omitempty"`
	Submissions []Submission `json:",omitempty"`
	Type        string       `json:",omitempty"`
	Updated     *time.Time   `json:",omitempty"`
}

type RoleInfo struct {
	Name     string `json:",omitempty"`
	MoodleId int64  `json:"-"`
}

type GradeInfo struct {
	Grade      float64     `json:",omitempty"`
	GradeMin   float64     `json:",omitempty"`
	GradeMax   float64     `json:",omitempty"`
	Assignment *Assignment `json:",omitempty"`
	Excluded   bool
	Updated    *time.Time `json:",omitempty"`
}

type ByCourseCode []Course

func (a ByCourseCode) Len() int      { return len(a) }
func (a ByCourseCode) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a ByCourseCode) Less(i, j int) bool {
	return a[i].Code < a[j].Code
}

// Get Moodle Account details matching by username
func (m *MoodleApi) GetPersonByUsername(username string) (*Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&field=username&values[0]=%s", m.base, m.token, "core_user_get_users_by_field",
		url.QueryEscape(username))
	body, err := GetUrl(url)

	if err != nil {
		m.log.Error("GetPersonByUsername() failed: %v", err)
		m.log.Error("GetPersonByUsername() url: %s", url)
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Result struct {
		Id           int64         `json:"id"`
		FirstName    string        `json:"firstname"`
		LastName     string        `json:"lastname"`
		Email        string        `json:"email"`
		Username     string        `json:"username"`
		CustomFields []CustomField `json:"customfields"`
	}

	var results []Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		fmt.Println(body)
		m.log.Error("GetPersonByUsername() failed parsing response: %v", err)
		return nil, err
	}

	people := make([]Person, 0, len(results))
	for _, i := range results {
		p := Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username}
		for _, c := range i.CustomFields {
			if c.Name == "alphacrucisid" {
				p.AlphacrucisId = c.Value
			}
			if c.Name == "personalemail" {
				p.PersonalEmail = c.Value
			}
		}
		people = append(people, p)
	}

	if len(people) == 0 {
		return nil, nil
	}
	if len(people) == 1 {
		return &people[0], nil
	}

	return nil, errors.New("Multiple moodle accounts match this username")
}

// Set the password for a moodle account. Password must match moodle password policy.
func (m *MoodleApi) ResetPassword(moodleId int64, password string) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][id]=%d&users[0][password]=%s", m.base, m.token, "core_user_update_users", moodleId,
		url.QueryEscape(password))
	body, err := GetUrl(url)

	if err != nil {
		m.log.Error("ResetPassword() failed: %v", err)
		m.log.Error("ResetPassword() url: %s", url)
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return errors.New(body)
	}

	m.log.Debug("Reset Password response: %s", body)

	return nil
}

// Fetch moodle account matching by email address.
func (m *MoodleApi) GetPersonByEmail(email string) (*Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&field=email&values[0]=%s", m.base, m.token, "core_user_get_users_by_field",
		url.QueryEscape(email))
	body, err := GetUrl(url)

	if err != nil {
		m.log.Error("GetPersonByEmail() failed: %v", err)
		m.log.Error("GetPersonByEmail() url: %s", url)
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Result struct {
		Id           int64         `json:"id"`
		FirstName    string        `json:"firstname"`
		LastName     string        `json:"lastname"`
		Email        string        `json:"email"`
		Username     string        `json:"username"`
		CustomFields []CustomField `json:"customfields"`
	}

	var results []Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		fmt.Println(body)
		m.log.Error("GetPersonByEmail() failed parsing: %v", err)
		return nil, err
	}

	people := make([]Person, 0, len(results))
	for _, i := range results {
		p := Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username}
		for _, c := range i.CustomFields {
			if c.Name == "alphacrucisid" {
				p.AlphacrucisId = c.Value
			}
			if c.Name == "personalemail" {
				p.PersonalEmail = c.Value
			}
		}
		people = append(people, p)
	}

	if len(people) == 0 {
		return nil, nil
	}
	if len(people) == 1 {
		return &people[0], nil
	}

	return nil, errors.New("Multiple moodle accounts match this email address")
}

// Reset the password for a moodle account, and email the password to the user
func (m *MoodleApi) ResetPasswordWithEmail(email string) error {
	p, err := m.GetPersonByEmail(email)
	if err != nil {
		return err
	}
	if p == nil {
		m.log.Debug("ResetPasswordWithEmail() failed. No user with email: %s", email)
		return errors.New("Email address not found in moodle")
	}

	pwd := RandomString(4) + "-1" + RandomString(4)
	err = m.ResetPassword(p.MoodleId, pwd)
	if err != nil {
		return err
	}

	if m.smtpHost == "" || m.smtpPort == 0 {
		return errors.New("ResetPasswordWithEmail() requires smtp host and port to be specified.")
	}
	if m.smtpUser == "" || m.smtpPassword == "" {
		return errors.New("ResetPasswordWithEmail() requires smtp user and password to be specified.")
	}
	if m.smtpFromName == "" || m.smtpFromEmail == "" {
		return errors.New("ResetPasswordWithEmail() requires smtp from name and email to be specified.")
	}

	var w bytes.Buffer
	w.Write([]byte(fmt.Sprintf("From: %s <%s>\r\n", m.smtpFromName, m.smtpFromEmail)))
	w.Write([]byte(fmt.Sprintf("To: %s\r\n", p.FirstName+" "+p.LastName+" <"+p.Email+">")))
	w.Write([]byte(fmt.Sprintf("Subject: Welcome to the Planetshakers College moodle\r\n")))
	w.Write([]byte("Content-Type: text/plain; charset=utf-8; format=flowed\r\n"))
	w.Write([]byte("Content-Transfer-Encoding: 8bit\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("Hi " + p.FirstName + ",\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("Welcome to the Planetshakers College Moodle, You can sign-in using the details below:\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("    URL: " + m.base + "\r\n"))
	w.Write([]byte("    Username: " + p.Email + "\r\n"))
	w.Write([]byte("    Password: " + pwd + "\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("If you have any difficulties with moodle access, please contact college@planetshakers.com\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("God bless,\r\n"))
	w.Write([]byte("Planetshakers College\r\n"))
	w.Write([]byte("\r\n"))
	msg := w.Bytes()
	fmt.Println(string(msg))

	var auth smtp.Auth
	if m.smtpUser != "" && m.smtpPassword != "" {
		auth = smtp.PlainAuth("", m.smtpUser, m.smtpPassword, m.smtpHost)
	}

	// TLS config
	tlsconfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         m.smtpHost,
	}

	// Here is the key, you need to call tls.Dial instead of smtp.Dial
	// for smtp servers running on 465 that require an ssl connection
	// from the very beginning (no starttls)
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", m.smtpHost, m.smtpPort), tlsconfig)
	if err != nil {
		return err
	}

	c, err := smtp.NewClient(conn, m.smtpHost)
	if err != nil {
		return err
	}

	if err = c.Auth(auth); err != nil {
		return err
	}

	if err = c.Mail(m.smtpFromEmail); err != nil {
		return err
	}

	if err = c.Rcpt(p.Email); err != nil {
		return err
	}

	w1, err := c.Data()
	if err != nil {
		return err
	}

	_, err = w1.Write([]byte(msg))
	if err != nil {
		return err
	}

	err = w1.Close()
	if err != nil {
		return err
	}

	c.Quit()

	return nil
}

// Reset the password for a moodle account, and email the password to the user
func (m *MoodleApi) WritingResetPasswordWithEmail(email string) error {
	p, err := m.GetPersonByEmail(email)
	if err != nil {
		return err
	}
	if p == nil {
		m.log.Debug("ResetPasswordWithEmail() failed. No user with email: %s", email)
		return errors.New("Email address not found in moodle")
	}

	pwd := RandomString(4) + "-1" + RandomString(4)
	err = m.ResetPassword(p.MoodleId, pwd)
	if err != nil {
		return err
	}

	if m.smtpHost == "" || m.smtpPort == 0 {
		return errors.New("ResetPasswordWithEmail() requires smtp host and port to be specified.")
	}
	if m.smtpUser == "" || m.smtpPassword == "" {
		return errors.New("ResetPasswordWithEmail() requires smtp user and password to be specified.")
	}
	if m.smtpFromName == "" || m.smtpFromEmail == "" {
		return errors.New("ResetPasswordWithEmail() requires smtp from name and email to be specified.")
	}

	var w bytes.Buffer
	w.Write([]byte(fmt.Sprintf("From: %s <%s>\r\n", m.smtpFromName, m.smtpFromEmail)))
	w.Write([]byte(fmt.Sprintf("To: %s\r\n", p.FirstName+" "+p.LastName+" <"+p.Email+">")))
	w.Write([]byte(fmt.Sprintf("Subject: Welcome to RES101\r\n")))
	w.Write([]byte("Content-Type: text/plain; charset=utf-8; format=flowed\r\n"))
	w.Write([]byte("Content-Transfer-Encoding: 8bit\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("Hi " + p.FirstName + ",\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("Welcome to the Planetshakers College Moodle, You now have access to RES101 in\r\n"))
	w.Write([]byte("Moodle. You can sign-in using the details below:\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("    URL: " + m.base + "\r\n"))
	w.Write([]byte("    Username: " + p.Email + "\r\n"))
	w.Write([]byte("    Password: " + pwd + "\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("God bless,\r\n"))
	w.Write([]byte("Planetshakers College\r\n"))
	w.Write([]byte("\r\n"))
	msg := w.Bytes()
	fmt.Println(string(msg))

	var auth smtp.Auth
	if m.smtpUser != "" && m.smtpPassword != "" {
		auth = smtp.PlainAuth("", m.smtpUser, m.smtpPassword, m.smtpHost)
	}

	// TLS config
	tlsconfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         m.smtpHost,
	}

	// Here is the key, you need to call tls.Dial instead of smtp.Dial
	// for smtp servers running on 465 that require an ssl connection
	// from the very beginning (no starttls)
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", m.smtpHost, m.smtpPort), tlsconfig)
	if err != nil {
		return err
	}

	c, err := smtp.NewClient(conn, m.smtpHost)
	if err != nil {
		return err
	}

	if err = c.Auth(auth); err != nil {
		return err
	}

	if err = c.Mail(m.smtpFromEmail); err != nil {
		return err
	}

	if err = c.Rcpt(p.Email); err != nil {
		return err
	}

	w1, err := c.Data()
	if err != nil {
		return err
	}

	_, err = w1.Write([]byte(msg))
	if err != nil {
		return err
	}

	err = w1.Close()
	if err != nil {
		return err
	}

	c.Quit()

	return nil
}

// Fetch moodle accounts that match match by first and last name.
func (m *MoodleApi) GetPeopleByFirstNameLastName(firstname, lastname string) (*[]Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&criteria[0][key]=firstname&criteria[0][value]=%s&criteria[0][key]=lastname&criteria[0][value]=%s", m.base, m.token, "core_user_get_users",
		url.QueryEscape(firstname),
		url.QueryEscape(lastname))
	body, err := GetUrl(url)

	if err != nil {
		m.log.Error("GetPeopleByFirstNameLastName() failed fetching url: %v", err)
		m.log.Debug("GetPeopleByFirstNameLastName() url: %s", url)
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Result struct {
		Id           int64         `json:"id"`
		FirstName    string        `json:"firstname"`
		LastName     string        `json:"lastname"`
		Email        string        `json:"email"`
		Username     string        `json:"username"`
		CustomFields []CustomField `json:"customfields"`
	}
	type Results struct {
		People []Result `json:"users"`
		Total  int64    `json:"total"`
	}

	var results Results

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		fmt.Println(body)
		return nil, err
	}

	people := make([]Person, 0, len(results.People))
	for _, i := range results.People {
		if strings.ToLower(i.FirstName) == strings.ToLower(firstname) &&
			strings.ToLower(i.LastName) == strings.ToLower(lastname) {
			people = append(people, Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username})
		}
	}

	return &people, nil
}

// Fetch moodle accounts that have a specific field. For example: api.GetPersonByAttribute("firstname", "James")
func (m *MoodleApi) GetPeopleByAttribute(attribute, value string) (*[]Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&criteria[0][key]=%s&criteria[0][value]=%s", m.base, m.token, "core_user_get_users",
		url.QueryEscape(attribute),
		url.QueryEscape(value))
	body, err := GetUrl(url)

	//fmt.Println(url)
	if err != nil {
		m.log.Error("GetPeopleByAttribute() failed fetching url: %v", err)
		m.log.Debug("GetPeopleByAttribute() url: %s", url)
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Result struct {
		Id           int64         `json:"id"`
		FirstName    string        `json:"firstname"`
		LastName     string        `json:"lastname"`
		Email        string        `json:"email"`
		Username     string        `json:"username"`
		CustomFields []CustomField `json:"customfields"`
	}
	type Results struct {
		People []Result `json:"users"`
		Total  int64    `json:"total"`
	}

	var results Results

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		fmt.Println(body)
		return nil, err
	}

	people := make([]Person, 0, len(results.People))
	for _, i := range results.People {
		p := Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username}
		for _, c := range i.CustomFields {
			if c.Name == "alphacrucisid" {
				p.AlphacrucisId = c.Value
			}
			if c.Name == "personalemail" {
				p.PersonalEmail = c.Value
			}
		}
		people = append(people, p)
	}

	return &people, nil
}

func (m *MoodleApi) SetRole(personId int64, roleId int64, courseId int64) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&enrolments[0][roleid]=%d&enrolments[0][userid]=%d&enrolments[0][courseid]=%d", m.base, m.token, "enrol_manual_enrol_users", roleId, personId, courseId)

	m.log.Debug("SetRole() url: %s", url)
	body, err := GetUrl(url)
	if err != nil {
		m.log.Error("SetRole() http request failed: %v", err)
		m.log.Debug("SetRole() url: %s", url)
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return errors.New(body)
	}

	return nil
}

func (m *MoodleApi) SetUserAttribute(personId int64, attribute, value string) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][id]=%d&users[0][%s]=%s", m.base, m.token, "core_user_update_users", personId,
		url.QueryEscape(attribute),
		url.QueryEscape(value),
	)

	body, err := GetUrl(url)
	//m.log.Debug("SetUserAttribute() url: %s", url)

	if err != nil {
		m.log.Error("SetUserAttribute() http request failed: %v", err)
		m.log.Debug("SetUserAttribute() url: %s", url)
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return errors.New(body)
	}

	return nil
}

func (m *MoodleApi) SetUserCustomField(personId int64, attribute, value string) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][id]=%d&users[0][customfields][0][type]=%s&users[0][customfields][0][value]=%s", m.base, m.token, "core_user_update_users", personId,
		url.QueryEscape(attribute),
		url.QueryEscape(value),
	)

	body, err := GetUrl(url)
	//m.log.Debug("SetUserCustomField() url: %s", url)

	if err != nil {
		m.log.Error("SetUserCustomField() http request failed: %v", err)
		m.log.Debug("SetUserCustomField() url: %s", url)
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return errors.New(body)
	}

	return nil
}

func (m *MoodleApi) AddPersonToCourseGroup(personId int64, groupId int64) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&members[0][userid]=%d&members[0][groupid]=%d", m.base, m.token, "core_group_add_group_members", personId, groupId)

	body, err := GetUrl(url)
	if err != nil {
		m.log.Error("SetRoleGroup() http request failed: %v", err)
		m.log.Debug("SetRoleGroup() url: %s", url)
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return errors.New(body)
	}

	type SiteInfo struct {
		Sitename  string
		Firstname string
		Lastname  string
		Userid    int64
	}

	var data map[string]interface{}

	if err := json.Unmarshal([]byte(body), &data); err != nil {
		fmt.Println(body)
		return err
	}

	return nil
}

func (m *MoodleApi) AddUser(firstName, lastName, email, username string) error {

	if strings.Index(email, "@") < 0 {
		return errors.New("Invalid email address")
	}
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][firstname]=%s&users[0][lastname]=%s&users[0][email]=%s&users[0][username]=%s&users[0][createpassword]=1", m.base, m.token, "core_user_create_users",
		url.QueryEscape(firstName),
		url.QueryEscape(lastName),
		url.QueryEscape(email),
		url.QueryEscape(username))

	body, err := GetUrl(url)
	fmt.Println(body)
	if err != nil {
		//fmt.Println(url)
		m.log.Error("%v", err)
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return errors.New(body)
	}

	type SiteInfo struct {
		Sitename  string
		Firstname string
		Lastname  string
		Userid    int64
	}

	var data map[string]interface{}

	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return err
	}

	return nil
}

type CourseGroup struct {
	Id        int64  `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"shortname"`
}

func (m *MoodleApi) GetCourseGroups(courseId int64) (*[]CourseGroup, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&courseid=%d", m.base, m.token, "core_group_get_course_groups", courseId)
	body, err := GetUrl(url)

	if err != nil {
		m.log.Error("GetCourseGroups failed fetching URL: %v", err)
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	var results []CourseGroup

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		fmt.Println(body)
		return nil, err
	}

	return &results, nil
}

type CustomField struct {
	Name  string `json:"shortname"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type CoursePerson struct {
	Id           int64         `json:"id"`
	Username     string        `json:"username"`
	FirstName    string        `json:"firstname"`
	LastName     string        `json:"lastname"`
	Email        string        `json:"email"`
	Groups       []CourseGroup `json:"groups"`
	Roles        []CourseGroup `json:"roles"`
	CustomFields []CustomField `json:"customfields"`
}

func (m *MoodleApi) GetCourseRoles(courseId int64) (*[]CoursePerson, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&courseid=%d", m.base, m.token, "core_enrol_get_enrolled_users", courseId)
	body, err := GetUrl(url)

	if err != nil {
		m.log.Error("GetCourseRoles() failed: %v", err)
		m.log.Error("GetCourseRoles() url: %s", url)
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	var results []CoursePerson

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		fmt.Println(body)
		return nil, err
	}

	return &results, nil
}

func (m *MoodleApi) GetCourses(value string) (*[]Course, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&criterianame=search&criteriavalue=%s", m.base, m.token, "core_course_search_courses", value)
	body, err := GetUrl(url)

	if err != nil {
		m.log.Error("%v", err)
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Result struct {
		Id   int64  `json:"id"`
		Code string `json:"shortname"`
		Name string `json:"fullname"`
	}
	type Results struct {
		Courses []Result `json:"courses"`
		Total   int64    `json:"total"`
	}

	var results Results

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		fmt.Println(body)
		return nil, err
	}

	subjects := make([]Course, 0, len(results.Courses))
	for _, i := range results.Courses {
		subjects = append(subjects, Course{MoodleId: i.Id, Code: i.Code, Name: i.Name})
	}
	sort.Sort(ByCourseCode(subjects))

	return &subjects, nil
}

func (m *MoodleApi) GetSiteInfo() (string, string, string, int64, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json", m.base, m.token, "core_webservice_get_site_info")

	body, err := GetUrl(url)

	if err != nil {
		m.log.Error("%v", err)
		return "", "", "", 0, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return "", "", "", 0, errors.New(body)
	}

	type SiteInfo struct {
		Sitename  string
		Firstname string
		Lastname  string
		Userid    int64
	}

	var data map[string]interface{}

	if err := json.Unmarshal([]byte(body), &data); err != nil {
		fmt.Println(body)
		return "", "", "", 0, err
	}

	return data["sitename"].(string), data["firstname"].(string), data["lastname"].(string), int64(data["userid"].(float64)), nil
}

func GetUrl(url string) (string, error) {

	timeout := time.Duration(25 * time.Second)
	client := http.Client{
		Transport: &http.Transport{
			Dial: func(netw, addr string) (net.Conn, error) {
				deadline := time.Now().Add(15 * time.Second)
				c, err := net.DialTimeout(netw, addr, time.Second*5)
				if err != nil {
					return nil, err
				}
				c.SetDeadline(deadline)
				return c, nil
			},
		},
		Timeout: timeout,
	}

	res, err := client.Get(url)
	if err != nil {
		if strings.Contains(err.Error(), "dial tcp: i/o timeout") {
			return "", errors.New("Timout connecting to The Age API")
		}
		if strings.Contains(err.Error(), "use of closed network connection") {
			return "", errors.New("Timout checking The Age")
		}
		return "", err
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return string(body), err
	}

	return string(body), err
}

func GetAttendance() error {

	// Get attendance for a session
	//https://learn.example.com/moodle/webservice/rest/server.php?wstoken=dd84ebafa3679c30bd65b9104a5f0f37&wsfunction=mod_wsattendance_get_session&moodlewsrestformat=json&sessionid=116

	// But how to we know which sessions to look at?

	return nil
}
