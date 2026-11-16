"""BM25 word-search baseline over already permitted knowledge cards.

This is an offline experiment, not an answer generator. The Go knowledge loader
remains the authority for production access checks. No external API is called.
"""

from __future__ import annotations

import json
import math
import re
from collections import Counter
from dataclasses import dataclass
from datetime import date
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
KNOWLEDGE_PATH = ROOT / "data" / "knowledge.json"
EVAL_PATH = ROOT / "data" / "eval" / "questions.json"
STOPWORDS = {
    "а", "без", "бы", "в", "где", "для", "до", "его", "еще", "за",
    "и", "из", "или", "как", "к", "ли", "мне", "мой", "можно", "на",
    "не", "но", "о", "он", "по", "под", "при", "с", "со", "у", "что",
    "это", "я",
}


@dataclass(frozen=True)
class SearchHit:
    card_id: str
    score: float


def words(text: str) -> list[str]:
    """Lowercase and split Russian/English text, without stemming or synonyms."""
    normalized = text.lower().replace("ё", "е")
    return [word for word in re.findall(r"[a-zа-я0-9]+", normalized) if word not in STOPWORDS]


def search(question: str, eligible_cards: list[dict], top_k: int = 3) -> list[SearchHit]:
    """Rank only cards supplied by an access-and-TTL filter.

    Search scores identify candidates, not permission to answer or act.
    A zero-overlap question returns no hits instead of an arbitrary card.
    """
    if top_k < 1 or not eligible_cards:
        return []
    query_terms = set(words(question))
    if not query_terms:
        return []

    documents = []
    frequencies = Counter()
    for card in eligible_cards:
        counts = Counter(words(card["evidence_text"] + " " + card["answer"]))
        counts.update({term: 2 * count for term, count in Counter(words(card["title"])).items()})
        documents.append((card["id"], counts))
        frequencies.update(counts.keys())

    count = len(documents)
    average_length = sum(sum(terms.values()) for _, terms in documents) / count
    hits = []
    for card_id, terms in documents:
        length = sum(terms.values())
        score = 0.0
        for term in query_terms:
            term_frequency = terms.get(term, 0)
            if not term_frequency:
                continue
            document_frequency = frequencies[term]
            idf = math.log1p((count - document_frequency + 0.5) / (document_frequency + 0.5))
            score += idf * (term_frequency * 2.2) / (
                term_frequency + 1.2 * (0.25 + 0.75 * length / average_length)
            )
        if score > 0:
            hits.append(SearchHit(card_id, round(score, 4)))

    return sorted(hits, key=lambda hit: (-hit.score, hit.card_id))[:top_k]


def offline_demo_context(role: str, team: str, as_of: date) -> list[dict]:
    """Mirror eligibility for local experiments; Go enforces it at runtime."""
    records = json.loads(KNOWLEDGE_PATH.read_text(encoding="utf-8"))["records"]
    return [
        {
            "id": card["id"],
            "title": card["title"],
            "evidence_text": card["evidence_text"],
            "answer": card["answer"],
        }
        for card in records
        if card["status"] == "approved"
        and card["expires_at"] is not None
        and date.fromisoformat(card["expires_at"]) >= as_of
        and card["approval_scope"] == "demo_only"
        and role in card["allowed_roles"]
        and card["team_scope"] in (team, "all-demo-teams")
    ]


def show_development_results() -> None:
    """Show candidate IDs for safe development questions, without their text."""
    dataset = json.loads(EVAL_PATH.read_text(encoding="utf-8"))
    as_of = date.fromisoformat(dataset["evaluation_time"][:10])
    for case in dataset["cases"]:
        if case["split"] != "development":
            continue
        if case["expected"].get("model_call_allowed", True) is False:
            continue
        cards = offline_demo_context(case["role"], "backend-platform-demo", as_of)
        hits = search(case["question"], cards)
        display = ", ".join(f"{hit.card_id}:{hit.score:.2f}" for hit in hits) or "no lexical match"
        print(f'{case["id"]}: {display}')


if __name__ == "__main__":
    show_development_results()
