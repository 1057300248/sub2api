#!/usr/bin/env python3
"""Run the audited fork regressions and reject silently empty test selections."""
from __future__ import annotations

import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = {
    "service": [
        "TestCustomPatchLegacyCookieJarPrivacy",
        "TestCustomPatchLegacyCookieJarWriteProtection",
        "TestCustomPatchTicketDefaultAlignment",
        "TestCustomPatchTicketExpiryDefaults",
        "TestCustomPatchTicketCookieIsolation",
        "TestCustomPatchTicketPlanChangeRejectsPriorBucket",
        "TestCustomPatchTicketStatusRedactsCredentials",
        "TestCodexTicketHarvestCookiesFollowFreshnessWindow",
        "TestCodexTicketCookiesRefreshOnlyFromAcceptedState",
        "TestOpenAICodexTicketExpectedBlocksByPlanType",
        "TestOpenAICodexTicketExpectedLengthTeamVariant",
        "TestOpenAICodexTicketTeamVariantAccepts332Probe",
        "TestObserveCodexTicketResponseRotatesValidReturnedState",
        "TestObserveCodexTicketResponseSameStateKeepsTicket",
        "TestEnsureDeepSeekChatReasoningPlaceholders",
        "TestEnsureDeepSeekChatReasoningPlaceholders_OpenCodeZenUpstream",
        "TestForwardResponses_DeepSeekReasoningUsesOutboundModel",
        "TestForwardResponses_DeepSeekChatFallbackInjectsReasoningPlaceholderOnCacheMiss",
        "TestForwardResponses_DeepSeekChatFallbackKeepsCachedReasoningContent",
        "TestForwardResponses_NonDeepSeekChatFallbackDoesNotInjectReasoningPlaceholder",
        "TestAntigravityGeminiStreamKeepsCommentKeepaliveForOrdinaryClients",
        "TestAntigravityGeminiStreamSkipsCommentKeepaliveForGoGenai",
        "TestSeedanceNativeForwarding",
        "TestSeedanceStatusAndDelete",
        "TestSeedanceValidationAndCapability",
        "TestSeedancePreservesUpstreamErrorsWithoutRetry",
        "TestHarvestControlsFillRefreshBeforeOnLegacyJSON",
    ],
    "config": ["TestCustomPatchTicketConfigDefaults"],
    "handler/dto": ["TestCustomPatchLegacyCookieJarRedactedFromAccountDTO"],
    "repository": ["TestCustomPatchLegacyCookieJarUsesLockedDatabaseValue"],
}


def main() -> int:
    output_dir = Path(os.environ.get("RUNNER_TEMP", "/tmp")) / "custom-patch-results"
    output_dir.mkdir(parents=True, exist_ok=True)
    for package, expected in REQUIRED.items():
        pattern = "^(" + "|".join(re.escape(name) for name in expected) + ")$"
        command = ["go", "test", "-tags=unit", f"./internal/{package}",
                   "-run", pattern, "-count=1", "-timeout=10m", "-json"]
        passed: set[str] = set()
        failed: set[str] = set()
        skipped: set[str] = set()
        with (output_dir / f"{package.replace('/', '-')}.jsonl").open("w", encoding="utf-8") as log:
            with subprocess.Popen(command, cwd=ROOT / "backend", stdout=subprocess.PIPE,
                                  stderr=subprocess.STDOUT, text=True) as process:
                assert process.stdout is not None
                for line in process.stdout:
                    log.write(line)
                    print(line, end="", flush=True)
                    try:
                        event = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    name = event.get("Test")
                    action = event.get("Action")
                    if name and action == "pass":
                        passed.add(name)
                    elif action == "fail":
                        failed.add(name or package)
                    elif name and action == "skip":
                        skipped.add(name)
                return_code = process.wait()
        missing = set(expected) - passed
        if return_code or missing or failed or skipped:
            print(f"{package}: exit={return_code}, missing={sorted(missing)}, "
                  f"failed={sorted(failed)}, skipped={sorted(skipped)}", file=sys.stderr)
            return 1
        print(f"{package}: all {len(expected)} required tests passed; "
              f"{len(passed)} test/subtest pass events; no skips", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
