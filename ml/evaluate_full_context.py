"""Evaluate full-context source selection without exposing question text.

Preview (no API call): python3 ml/evaluate_full_context.py
Small live run: python3 ml/evaluate_full_context.py --run
One chosen case: python3 ml/evaluate_full_context.py --run --case V2-D24
Full development set: python3 ml/evaluate_full_context.py --run --all

The endpoint and model are configured by BUDDY_LLM_URL, BUDDY_LLM_MODEL and
BUDDY_LLM_API_KEY. Holdout questions are deliberately unavailable here.
"""

from __future__ import annotations

import argparse
import os
import time
from collections import Counter
from datetime import date

from evaluate_retrieval import load_and_validate
from full_context_selector import build_messages, select_with_model
from lexical_retrieval import offline_demo_context


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", action="store_true", help="make real model calls")
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--all", action="store_true", help="use all development cases instead of three")
    selection.add_argument("--case", help="run one development question by ID")
    args = parser.parse_args()

    dataset = load_and_validate()
    cases = [case for case in dataset["cases"] if case["split"] == "development"]
    if args.case:
        cases = [case for case in cases if case["id"] == args.case]
        if not cases:
            raise SystemExit("Choose an ID from the development split")
    elif not args.all:
        cases = cases[:3]
    as_of = date.fromisoformat(dataset["evaluation_time"][:10])
    team = dataset["context"]["team"]
    prepared = []
    for case in cases:
        if case["expected"].get("model_call_allowed", True) is False:
            raise ValueError(f'question {case["id"]} is not allowed for an external model')
        cards = offline_demo_context(case["role"], team, as_of)
        messages = build_messages(case["question"], cards)
        prepared.append((case, cards, sum(len(message["content"]) for message in messages)))

    print(f"Development cases: {len(prepared)}")
    print(f"Total prompt characters: {sum(size for _, _, size in prepared)}")
    print("Counts by expected outcome:", dict(Counter(case["expected"]["outcome"] for case, _, _ in prepared)))
    if not args.run:
        print("Preview only. No model call was made; pass --run to test a configured endpoint.")
        return

    url = os.getenv("BUDDY_LLM_URL", "")
    model = os.getenv("BUDDY_LLM_MODEL", "")
    api_key = os.getenv("BUDDY_LLM_API_KEY", "")
    if not url or not model:
        raise SystemExit("Set BUDDY_LLM_URL and BUDDY_LLM_MODEL before --run")

    counts = Counter()
    times = []
    for case, cards, _ in prepared:
        started = time.perf_counter()
        selection = select_with_model(case["question"], cards, url=url, model=model, api_key=api_key)
        elapsed_ms = (time.perf_counter() - started) * 1000
        times.append(elapsed_ms)
        expected = case["expected"]
        if expected["outcome"] == "answer":
            group = "answer_source"
            correct = selection.card_id in expected["source_ids"]
        else:
            group = "no_source"
            correct = selection.card_id is None
        counts[f"{group}_total"] += 1
        counts[f"{group}_correct"] += correct
        print(f'{case["id"]}: selected={selection.card_id or "none"}, correct={correct}, latency_ms={elapsed_ms:.0f}')

    for group in ("answer_source", "no_source"):
        total = counts[f"{group}_total"]
        if total:
            print(f"{group}: {counts[f'{group}_correct']}/{total}")
    print(f"Mean request latency: {sum(times) / len(times):.0f} ms")
    print("These scores test source selection, not factual correctness of a generated answer.")


if __name__ == "__main__":
    main()
