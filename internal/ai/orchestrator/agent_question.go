package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	maxUserQuestions       = 5
	maxQuestionOptions     = 12
	maxQuestionIDRunes     = 64
	maxQuestionPromptRunes = 600
	maxQuestionTitleRunes  = 160
)

type askUserArgs struct {
	Title     string               `json:"title"`
	Questions []askUserQuestionArg `json:"questions"`
}

type askUserQuestionArg struct {
	ID          string                      `json:"id"`
	Prompt      string                      `json:"prompt"`
	Type        domain.UserQuestionType     `json:"type"`
	Options     []domain.UserQuestionOption `json:"options"`
	AllowCustom *bool                       `json:"allow_custom"`
	Placeholder string                      `json:"placeholder"`
}

func parseUserQuestionArgs(raw string) (domain.UserQuestionRequest, error) {
	var args askUserArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return domain.UserQuestionRequest{}, fmt.Errorf("invalid arguments: %w", err)
	}

	title := strings.TrimSpace(args.Title)
	if len([]rune(title)) > maxQuestionTitleRunes {
		return domain.UserQuestionRequest{}, fmt.Errorf("title is too long")
	}
	if len(args.Questions) == 0 {
		return domain.UserQuestionRequest{}, fmt.Errorf("at least one question is required")
	}
	if len(args.Questions) > maxUserQuestions {
		return domain.UserQuestionRequest{}, fmt.Errorf("at most %d questions are allowed", maxUserQuestions)
	}

	seen := make(map[string]struct{}, len(args.Questions))
	questions := make([]domain.UserQuestion, 0, len(args.Questions))
	for _, input := range args.Questions {
		question, err := normalizeUserQuestion(input)
		if err != nil {
			return domain.UserQuestionRequest{}, err
		}
		if _, ok := seen[question.ID]; ok {
			return domain.UserQuestionRequest{}, fmt.Errorf("duplicate question id %q", question.ID)
		}
		seen[question.ID] = struct{}{}
		questions = append(questions, question)
	}

	return domain.UserQuestionRequest{Title: title, Questions: questions}, nil
}

func normalizeUserQuestion(input askUserQuestionArg) (domain.UserQuestion, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return domain.UserQuestion{}, fmt.Errorf("question id is required")
	}
	if len([]rune(id)) > maxQuestionIDRunes {
		return domain.UserQuestion{}, fmt.Errorf("question id %q is too long", id)
	}

	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return domain.UserQuestion{}, fmt.Errorf("question %q prompt is required", id)
	}
	if len([]rune(prompt)) > maxQuestionPromptRunes {
		return domain.UserQuestion{}, fmt.Errorf("question %q prompt is too long", id)
	}

	questionType := input.Type
	if questionType == "" {
		questionType = domain.UserQuestionSingleChoice
	}
	switch questionType {
	case domain.UserQuestionSingleChoice, domain.UserQuestionMultipleChoice, domain.UserQuestionText:
	default:
		return domain.UserQuestion{}, fmt.Errorf("question %q has invalid type %q", id, questionType)
	}

	options := normalizeQuestionOptions(input.Options)
	if questionType != domain.UserQuestionText && len(options) == 0 {
		return domain.UserQuestion{}, fmt.Errorf("question %q requires at least one option", id)
	}
	if len(options) > maxQuestionOptions {
		return domain.UserQuestion{}, fmt.Errorf("question %q has more than %d options", id, maxQuestionOptions)
	}

	allowCustom := true
	if input.AllowCustom != nil {
		allowCustom = *input.AllowCustom
	}

	return domain.UserQuestion{
		ID:          id,
		Prompt:      prompt,
		Type:        questionType,
		Options:     options,
		AllowCustom: allowCustom,
		Placeholder: strings.TrimSpace(input.Placeholder),
	}, nil
}

func normalizeQuestionOptions(options []domain.UserQuestionOption) []domain.UserQuestionOption {
	normalized := make([]domain.UserQuestionOption, 0, len(options))
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		value := strings.TrimSpace(option.Value)
		label := strings.TrimSpace(option.Label)
		if value == "" {
			continue
		}
		if label == "" {
			label = value
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, domain.UserQuestionOption{Value: value, Label: label})
	}
	return normalized
}
