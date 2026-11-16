"""Protocol tests for the full-context selector, using a local fake API."""

from __future__ import annotations

import json
import io
import socket
import ssl
import threading
import unittest
from contextlib import contextmanager
from datetime import date
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError, URLError
from unittest.mock import patch

from full_context_selector import select_with_model
from lexical_retrieval import offline_demo_context


@contextmanager
def fake_chat_api(card_id: str | None, *, finish_reason: str = "stop"):
    received: list[dict] = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            body = self.rfile.read(int(self.headers["Content-Length"]))
            received.append(json.loads(body))
            response = {
                "choices": [{
                    "finish_reason": finish_reason,
                    "message": {"content": json.dumps({"card_id": card_id})},
                }]
            }
            encoded = json.dumps(response).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, *args: object) -> None:
            pass  # The test server never logs request text.

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}/chat/completions", received
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


class FullContextSelectorTests(unittest.TestCase):
    def setUp(self) -> None:
        self.cards = offline_demo_context("intern", "backend-platform-demo", date(2026, 11, 16))
        self.question = "Как получить доступ к учебному репозиторию?"

    def test_sends_only_eligible_cards_and_accepts_their_id(self) -> None:
        with fake_chat_api("K-101") as (url, received):
            result = select_with_model(self.question, self.cards, url=url, model="fake-local-model")
        self.assertEqual(result.card_id, "K-101")
        self.assertEqual(len(received), 1)
        self.assertEqual(received[0]["model"], "fake-local-model")
        context = json.loads(received[0]["messages"][1]["content"])
        self.assertEqual({card["id"] for card in context["cards"]}, {card["id"] for card in self.cards})
        self.assertNotIn("K-103", {card["id"] for card in context["cards"]})  # expired
        self.assertNotIn("K-104", {card["id"] for card in context["cards"]})  # pending

    def test_rejects_id_not_sent_to_model(self) -> None:
        with fake_chat_api("K-103") as (url, _):
            with self.assertRaisesRegex(ValueError, "unavailable card"):
                select_with_model(self.question, self.cards, url=url, model="fake-local-model")

    def test_rejects_incomplete_model_response(self) -> None:
        with fake_chat_api("K-101", finish_reason="length") as (url, _):
            with self.assertRaisesRegex(ValueError, "incomplete"):
                select_with_model(self.question, self.cards, url=url, model="fake-local-model")

    def test_external_endpoint_requires_key_before_network_call(self) -> None:
        with self.assertRaisesRegex(ValueError, "requires an API key"):
            select_with_model(
                self.question, self.cards,
                url="https://example.org/v1/chat/completions", model="example-model",
            )

    def test_deepseek_uses_json_mode_and_bearer_auth(self) -> None:
        response = io.BytesIO(json.dumps({
            "choices": [{
                "finish_reason": "stop",
                "message": {"content": json.dumps({"card_id": "K-101"})},
            }]
        }).encode())
        with patch("full_context_selector.urllib.request.urlopen", return_value=response) as urlopen:
            result = select_with_model(
                self.question,
                self.cards,
                url="https://api.deepseek.com/chat/completions",
                model="deepseek-chat",
                api_key="test-key",
            )

        self.assertEqual(result.card_id, "K-101")
        request = urlopen.call_args.args[0]
        self.assertTrue(request.get_header("Authorization").startswith("Bearer "))
        self.assertTrue(request.get_header("Authorization").endswith("key"))
        self.assertEqual(json.loads(request.data)["response_format"], {"type": "json_object"})

    def test_provider_http_error_is_reported_without_exposing_response_body(self) -> None:
        response_body = b"secret-bearing provider error"
        error = HTTPError(
            "https://api.deepseek.com/chat/completions",
            401,
            "Unauthorized",
            {},
            io.BytesIO(response_body),
        )
        with patch("full_context_selector.urllib.request.urlopen", side_effect=error):
            with self.assertRaisesRegex(RuntimeError, "provider_http_401") as raised:
                select_with_model(
                    self.question,
                    self.cards,
                    url="https://api.deepseek.com/chat/completions",
                    model="deepseek-chat",
                    api_key="test-key",
                )
        self.assertNotIn("secret-bearing", str(raised.exception))

    def test_provider_network_error_is_classified_without_exposing_details(self) -> None:
        cases = [
            (socket.gaierror("sensitive hostname detail"), "provider_dns_error"),
            (ssl.SSLCertVerificationError("sensitive certificate detail"), "provider_tls_certificate_error"),
            (ssl.SSLError("sensitive TLS detail"), "provider_tls_error"),
        ]
        for reason, expected in cases:
            with self.subTest(expected=expected), patch(
                "full_context_selector.urllib.request.urlopen",
                side_effect=URLError(reason),
            ):
                with self.assertRaisesRegex(RuntimeError, expected) as raised:
                    select_with_model(
                        self.question,
                        self.cards,
                        url="https://api.deepseek.com/chat/completions",
                        model="deepseek-flash",
                        api_key="test-key",
                    )
                self.assertNotIn("sensitive", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
