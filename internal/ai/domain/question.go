package domain

// UserQuestionType identifies the kind of response expected for a user question.
type UserQuestionType string

const (
	UserQuestionSingleChoice   UserQuestionType = "single_choice"
	UserQuestionMultipleChoice UserQuestionType = "multiple_choice"
	UserQuestionText           UserQuestionType = "text"
)

// UserQuestionRequest asks the frontend to collect one or more answers from the user.
type UserQuestionRequest struct {
	ID        string         `json:"id"`
	Title     string         `json:"title,omitempty"`
	Questions []UserQuestion `json:"questions"`
}

// UserQuestion is one prompt within a user question request.
type UserQuestion struct {
	ID          string               `json:"id"`
	Prompt      string               `json:"prompt"`
	Type        UserQuestionType     `json:"type"`
	Options     []UserQuestionOption `json:"options,omitempty"`
	AllowCustom bool                 `json:"allowCustom"`
	Placeholder string               `json:"placeholder,omitempty"`
}

// UserQuestionOption is one selectable answer offered by the agent.
type UserQuestionOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// UserQuestionAnswer is the user's response to one question.
type UserQuestionAnswer struct {
	QuestionID string   `json:"questionId"`
	Values     []string `json:"values,omitempty"`
	Custom     string   `json:"custom,omitempty"`
	Skipped    bool     `json:"skipped,omitempty"`
}

// UserQuestionResult reports the resolved state of a user question request.
type UserQuestionResult struct {
	ID      string               `json:"id"`
	Status  string               `json:"status"`
	Answers []UserQuestionAnswer `json:"answers,omitempty"`
}
