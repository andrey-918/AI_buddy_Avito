"""Live source selection for questions screened and filtered by the Go API.

Input and output are one JSON object on stdin/stdout. Never print question text.
The Go knowledge loader is the authority for role, team, status and TTL.
"""

from __future__ import annotations

import json
import os
import re
import sys

from full_context_selector import select_with_model
from intent_baseline import classify_intent
from lexical_retrieval import search


def decide(question: str, cards: list[dict]) -> dict:
    intent = classify_intent(question).intent
    normalized = question.lower().replace("ё", "е")
    if re.search(r"staging|стейдж|тестов\w* стенд", normalized):
        return {"outcome": "human_gate", "intent": intent, "reason": "expired_source", "owner": "Platform Tools", "text": "Инструкция по staging требует повторной проверки. Передайте вопрос владельцу темы."}
    if re.search(r"мерж|\bmerge\b|согласован|разреши|одобри|утверди", normalized):
        return {"outcome": "human_gate", "intent": intent, "reason": "human_decision", "owner": "Наставник команды", "text": "Для этого нужно решение человека. Передайте вопрос наставнику или руководителю."}

    url = os.getenv("BUDDY_LLM_URL", "")
    model = os.getenv("BUDDY_LLM_MODEL", "")
    key = os.getenv("BUDDY_LLM_API_KEY", "")
    hits = search(question, cards, 3)
    if url and model and key:
        try:
            chosen = select_with_model(question, cards, url=url, model=model, api_key=key).card_id
        except RuntimeError as exc:
            diagnostics = {
                "provider_http_401": "провайдер отклонил API-ключ (HTTP 401)",
                "provider_http_402": "провайдер отклонил запрос из-за доступа или квоты (HTTP 402)",
                "provider_http_403": "у ключа нет доступа к этой модели (HTTP 403)",
                "provider_http_404": "не найден endpoint или модель (HTTP 404)",
                "provider_http_429": "исчерпана квота или превышен лимит запросов (HTTP 429)",
                "provider_timeout": "истекло время ожидания ответа",
                "provider_tls_certificate_error": "ошибка проверки TLS-сертификата",
                "provider_tls_error": "ошибка защищённого TLS-соединения",
                "provider_dns_error": "не удалось найти адрес API в DNS",
                "provider_connection_error": "не удалось подключиться к провайдеру",
                "provider_invalid_json": "провайдер вернул некорректный JSON",
            }
            detail = diagnostics.get(str(exc), "провайдер не смог обработать запрос")
            return {"outcome": "fallback", "intent": intent, "reason": "model_unavailable", "text": f"Модель сейчас недоступна: {detail}. Проверьте конфигурацию и попробуйте снова или передайте вопрос наставнику."}
        except ValueError:
            return {"outcome": "fallback", "intent": intent, "reason": "model_unavailable", "text": "Модель сейчас недоступна: ответ провайдера не прошёл проверку формата. Проверьте endpoint и модель или передайте вопрос наставнику."}
        mode = "llm_source_selection"
    else:
        # Conservative local baseline. The score is not a calibrated probability.
        chosen = hits[0].card_id if hits and hits[0].score >= 4.8 else None
        mode = "bm25_baseline"

    card = next((item for item in cards if item["id"] == chosen), None)
    if card is None:
        return {"outcome": "fallback", "intent": intent, "reason": "no_supported_source", "mode": mode, "text": "В доступной базе нет подтверждённого ответа. Можно передать вопрос наставнику."}
    return {
        "outcome": "answer", "intent": intent, "mode": mode,
        "card_id": card["id"], "text": card["answer"],
        "retrieval_score": next((hit.score for hit in hits if hit.card_id == chosen), None),
    }


def main() -> None:
    request = json.load(sys.stdin)
    print(json.dumps(decide(request["question"], request["cards"]), ensure_ascii=False))


if __name__ == "__main__":
    main()
