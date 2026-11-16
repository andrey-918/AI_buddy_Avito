"""Suggest existing knowledge for a mentor's new FAQ; a person resolves matches."""

from __future__ import annotations

import json
import sys
from difflib import SequenceMatcher

from lexical_retrieval import search, words


def candidates(question: str, cards: list[dict]) -> list[str]:
    """Return likely duplicate IDs, never merge or approve automatically."""
    lexical = {hit.card_id: hit.score for hit in search(question, cards, top_k=5)}
    query_terms = set(words(question))
    found: list[tuple[str, float]] = []
    for card in cards:
        title = card["title"]
        title_terms = set(words(title))
        overlap = len(query_terms & title_terms) / max(1, len(query_terms | title_terms))
        similarity = SequenceMatcher(None, question.lower(), title.lower()).ratio()
        score = max(lexical.get(card["id"], 0) / 8, overlap, similarity)
        if lexical.get(card["id"], 0) >= 3.5 or overlap >= 0.45 or similarity >= 0.65:
            found.append((card["id"], score))
    return [card_id for card_id, _ in sorted(found, key=lambda item: -item[1])[:3]]


def main() -> None:
    request = json.load(sys.stdin)
    print(json.dumps({"candidate_ids": candidates(request["question"], request["cards"])}))


if __name__ == "__main__":
    main()
