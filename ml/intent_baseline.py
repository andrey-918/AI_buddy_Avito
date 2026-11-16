"""Local, explainable intent baseline for safe questions.

Run after the Go guardrail. The CLI evaluates only development questions that
are allowed past that guardrail; it never calls an external model or API.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from pathlib import Path


EVAL_PATH = Path(__file__).resolve().parents[1] / "data" / "eval" / "questions.json"


@dataclass(frozen=True)
class IntentPrediction:
    intent: str
    rule: str
    confidence: float


def classify_intent(question: str) -> IntentPrediction:
    """Classify a question already cleared by the privacy guardrail.

    Confidence is a hand-assigned rule strength, not a trained probability.
    It must not be used as a threshold for giving a factual answer.
    """
    text = " ".join(question.lower().replace("ё", "е").split())

    # Specific topics win over broad words such as "где" or "сервис".
    if re.search(r"ценност|манифест|культур[а-я]* компан|принцип[а-я]* компан", text):
        return IntentPrediction("values", "company_values", 0.90)

    if re.search(r"\bmerge\b|мерж[а-я]*|слиян[а-я]* вет[а-я]*", text):
        return IntentPrediction("other", "code_approval", 0.85)

    if re.search(r"репозитор|адаптац|онбординг|перв[а-я]* недел|наставник", text):
        return IntentPrediction("onboarding", "onboarding_topic", 0.85)
    if re.search(r"(доступ|прав[а-я]*)[^.!?]*\bкод[а-я]*\b", text):
        return IntentPrediction("onboarding", "code_access", 0.75)

    if re.search(r"ci/cd|staging|командиров|сервис|инструкц", text):
        return IntentPrediction("navigation", "service_or_process", 0.80)
    if re.search(r"\b(где|куда|кому|кого|кто)\b", text):
        return IntentPrediction("navigation", "direction_question", 0.60)

    return IntentPrediction("other", "no_clear_signal", 0.30)


def evaluate_development_set() -> None:
    """Print predictions without printing or logging the question text."""
    dataset = json.loads(EVAL_PATH.read_text(encoding="utf-8"))
    correct = 0
    total = 0
    for case in dataset["cases"]:
        if case["split"] != "development":
            continue
        if case["expected"].get("model_call_allowed", True) is False:
            continue
        prediction = classify_intent(case["question"])
        expected = case["expected"]["intent"]
        correct += prediction.intent == expected
        total += 1
        print(
            f'{case["id"]}: expected={expected}, predicted={prediction.intent}, '
            f'rule={prediction.rule}, heuristic_confidence={prediction.confidence:.2f}'
        )
    print(f"Development accuracy: {correct}/{total}")
    print("This small development score is not an estimate of real-world accuracy.")


if __name__ == "__main__":
    evaluate_development_set()
