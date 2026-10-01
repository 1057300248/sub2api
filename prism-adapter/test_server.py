import importlib.util
import json
import tempfile
import threading
import unittest
from pathlib import Path
from http.server import ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.request import Request, urlopen


spec = importlib.util.spec_from_file_location("prism_adapter", Path(__file__).with_name("server.py"))
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class AdapterTests(unittest.TestCase):
    def test_text_request_keeps_model_and_stream(self):
        prompt, stream = adapter.parse_prompt({
            "model": "gpt-5.6-sol", "stream": True,
            "instructions": "Answer exactly.",
            "input": [{"role": "user", "content": [{"type": "input_text", "text": "hi"}]}],
        })
        self.assertEqual(prompt, "[instructions]\nAnswer exactly.\n\n[user]\nhi")
        self.assertTrue(stream)

    def test_unsupported_features_fail_closed(self):
        for change in ({"model": "gpt-6-astra"}, {"tools": [{"type": "function", "name": "x"}]},
                       {"previous_response_id": "resp_1"}, {"reasoning": {"effort": "high"}}):
            request = {"model": "gpt-5.6-sol", "input": "hi", **change}
            with self.assertRaises(adapter.AdapterError):
                adapter.parse_prompt(request)

    def test_terminal_output_and_unknown_state(self):
        self.assertIsNone(adapter.terminal_text({"status": "running"}))
        self.assertEqual(adapter.terminal_text({"status": "completed", "response": {
            "status": "success", "payload": {"output": [{"type": "message", "content": [{"text": "21"}]}]}
        }}), "21")

    def test_pending_journal_blocks_ambiguous_replay(self):
        with tempfile.TemporaryDirectory() as directory:
            state = adapter.State(directory)
            state.begin("300")
            with self.assertRaises(adapter.AdapterError) as raised:
                state.begin("300")
            self.assertEqual(raised.exception.status, 409)
            state.finish("300")
            state.begin("300")
            state.finish("300")

    def test_http_boundary_uses_real_terminal_without_usage(self):
        class FakeBrowser:
            def run(self, account_id, token, prompt):
                self.assert_values = (account_id, token, prompt)
                return "prism-123", "21"

        fake = FakeBrowser()
        handler = type("TestHandler", (adapter.Handler,), {"api_key": "test-key", "browser_turn": fake})
        server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        server.daemon_threads = True
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        try:
            url = f"http://127.0.0.1:{server.server_port}/v1/responses"
            data = json.dumps({"model": "gpt-5.6-sol", "input": "candy"}).encode()
            headers = {"Authorization": "Bearer test-key", "X-Prism-Account-ID": "300",
                       "X-Prism-OAuth-Token": "oauth-token", "Content-Type": "application/json"}
            with urlopen(Request(url, data=data, headers=headers), timeout=5) as response:
                body = json.load(response)
            self.assertEqual(fake.assert_values, ("300", "oauth-token", "[user]\ncandy"))
            self.assertEqual(body["output"][0]["content"][0]["text"], "21")
            self.assertIsNone(body["usage"])
            with self.assertRaises(HTTPError) as denied:
                urlopen(Request(url, data=data, headers={"Content-Type": "application/json"}), timeout=5)
            self.assertEqual(denied.exception.code, 401)
        finally:
            server.shutdown()
            server.server_close()

    def test_gate_blocks_retry_and_model_downgrade_before_send(self):
        gate = adapter.StartGate()
        body = {"metadata": {"model": "gpt-5.6-sol", "reasoning_effort": "medium"}}
        self.assertFalse(gate.accept(body))
        gate = adapter.StartGate()
        gate.armed = True
        self.assertTrue(gate.accept(body))
        self.assertFalse(gate.accept(body))
        for metadata in ({"model": "gpt-6-astra", "reasoning_effort": "medium"},
                         {"model": "gpt-5.6-sol", "reasoning_effort": "high"}):
            gate = adapter.StartGate()
            gate.armed = True
            self.assertFalse(gate.accept({"metadata": metadata}))
            self.assertFalse(gate.sent)

    def test_success_is_not_terminal_without_completed_status(self):
        self.assertIsNone(adapter.terminal_text({"status": "running", "response": {"status": "success"}}))
        self.assertIsNone(adapter.terminal_text({"status": "completed"}))
        self.assertIsNone(adapter.terminal_text({"status": "completed", "response": "invalid"}))

    def test_journal_retains_latest_turn_state_and_writes_redacted_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            state = adapter.State(directory)
            state.begin("300", "fixture-project")
            state.update("300", {"request_id": "fixture-request", "turn_state": "fixture-state-one"})
            state.update("300", {"turn_state": "fixture-state-two"})
            journal = json.loads((state.pending / "300").read_text())
            self.assertEqual(journal["turn_state"], "fixture-state-two")
            self.assertEqual(journal["request_id"], "fixture-request")
            state.receipt("300", "fixture-request", 1, 2, "secret answer text")
            receipt = next(state.receipts.iterdir()).read_text()
            self.assertNotIn("secret answer text", receipt)
            self.assertNotIn("turn_state", receipt)
            self.assertEqual(json.loads(receipt)["start_count"], 1)

    def test_unsupported_options_and_empty_text_do_not_dispatch(self):
        for fields in ({"additional_tools": [{"name": "shell"}]}, {"background": True},
                       {"max_output_tokens": 10}, {"input": "   "}, {"store": True},
                       {"text": {"format": {"type": "json_schema"}}}):
            with self.assertRaises(adapter.AdapterError):
                adapter.parse_prompt({"model": adapter.MODEL, "input": "hi", **fields})


if __name__ == "__main__":
    unittest.main()
