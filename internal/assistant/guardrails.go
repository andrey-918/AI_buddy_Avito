package assistant

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxQuestionLength = 2000

// GuardResult contains only a decision and a safe reply. It never retains the
// original question, so callers can log the reason without logging the text.
type GuardResult struct {
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}

var (
	emailPattern      = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phonePattern      = regexp.MustCompile(`(?:^|\D)(?:\+7|8)[\s().-]*\d{3}[\s().-]*\d{3}[\s().-]*\d{2}[\s().-]*\d{2}(?:\D|$)`)
	longNumberPattern = regexp.MustCompile(`\b(?:\d[ -]?){10,19}\b`)
	// Go's \w and \b are ASCII-only; Russian word endings need a Cyrillic class.
	sensitiveTermPattern   = regexp.MustCompile(`паспортн[а-я]* данн[а-я]*|паспорт сотрудник[а-я]*|снилс|дата рождени[а-я]*|адрес проживани[а-я]*|номер карт[а-я]*|зарплат[а-я]*|оклад[а-я]*|медицинск[а-я]* данн[а-я]*|сколько зарабатыва[а-я]*|доход[а-я]* коллег[а-я]*`)
	financialActionPattern = regexp.MustCompile(`перевед[иу]|перечисл[а-я]*|выплат[а-я]*|оплат[иь][а-я]*|провед[а-я]* платеж[а-я]*`)
	financialObjectPattern = regexp.MustCompile(`деньг[а-я]*|денежн[а-я]*|средств[а-я]*|счет[а-я]*|карт[а-я]*|платеж[а-я]*|зарплат[а-я]*`)
	personalActionPattern  = regexp.MustCompile(`отправ[а-я]*|загруз[а-я]*|переда[а-я]*|передай|сохран[а-я]*|обработ[а-я]*|покаж[а-я]*|выдай|раскрой`)
	excludedPattern        = regexp.MustCompile(`индивидуальн[а-я]* план[а-я]* развити[а-я]*|развивающ[а-я]* обратн[а-я]* связ[а-я]*|оцени[а-я]* мо[а-я]* работ[а-я]*|дай[а-я]* обратн[а-я]* связ[а-я]*|провед[а-я]* ревью|сделай[а-я]* код[- ]?ревью|психолог[а-я]*|психотерап[а-я]*|депресс[а-я]*|паническ[а-я]*|мне тревожно`)
)

// ScreenQuestion is a local baseline for topics that must stop before any
// external embedding or language-model API. Rules are intentionally
// conservative; they are not a complete personal-data detector.
func ScreenQuestion(question string) GuardResult {
	question = strings.TrimSpace(question)
	if question == "" || utf8.RuneCountInString(question) > maxQuestionLength {
		return GuardResult{Reason: "invalid_question", Fallback: "Сформулируйте короткий вопрос без личных данных."}
	}
	text := strings.ToLower(strings.ReplaceAll(question, "ё", "е"))
	text = strings.Join(strings.Fields(text), " ")
	if emailPattern.MatchString(text) || phonePattern.MatchString(text) || longNumberPattern.MatchString(text) ||
		sensitiveTermPattern.MatchString(text) ||
		((strings.Contains(text, "персональн") || strings.Contains(text, "пдн")) && personalActionPattern.MatchString(text)) {
		return GuardResult{Reason: "sensitive_data", Fallback: "Я не обрабатываю личные данные. Уберите их из вопроса и обратитесь к ответственному через корпоративный процесс."}
	}
	if financialActionPattern.MatchString(text) && financialObjectPattern.MatchString(text) {
		return GuardResult{Reason: "financial_operation", Fallback: "Я не выполняю финансовые операции. Обратитесь к ответственному через корпоративный процесс."}
	}
	if excludedPattern.MatchString(text) || hasToken(text, "ипр") {
		return GuardResult{Reason: "excluded_scope", Fallback: "Этот вопрос требует участия наставника, руководителя или профильного специалиста. AI-buddy не принимает такое решение."}
	}
	return GuardResult{Allowed: true}
}

func hasToken(text, token string) bool {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'а' && r <= 'я') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	for _, word := range words {
		if word == token {
			return true
		}
	}
	return false
}
