"""
E2E tests for Go DensePose Sensing Server.

Tests the Go server's HTTP API and WebSocket endpoints from Python.
Requires the Go server to be running on localhost:3000.
"""

import json
import subprocess
import sys
import time
import os
import signal
import urllib.request
import urllib.error

# Try to import websocket-client; skip WS tests if missing
try:
    import websocket
    HAS_WS = True
except ImportError:
    HAS_WS = False

GO_SERVER_ADDR = os.environ.get("GO_SERVER_ADDR", "localhost:3000")
BASE_URL = f"http://{GO_SERVER_ADDR}"
WS_URL = f"ws://{GO_SERVER_ADDR}/ws/sensing"

_server_process = None


def start_go_server():
    """Build and start the Go server in background."""
    global _server_process
    go_dir = os.path.join(os.path.dirname(__file__), "..", "..")
    go_dir = os.path.abspath(go_dir)

    print(f"[e2e] Building Go server in {go_dir}...")
    result = subprocess.run(
        ["go", "build", "-o", "densepose-test", "./cmd/densepose/"],
        cwd=go_dir,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        print(f"[e2e] Build failed:\n{result.stderr}")
        return False

    print("[e2e] Starting Go server...")
    _server_process = subprocess.Popen(
        ["./densepose-test", "-source", "simulated", "-port", "3000"],
        cwd=go_dir,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    for attempt in range(30):
        time.sleep(0.5)
        try:
            urllib.request.urlopen(f"{BASE_URL}/health", timeout=1)
            print(f"[e2e] Server ready after {(attempt+1)*0.5:.1f}s")
            return True
        except (urllib.error.URLError, ConnectionRefusedError):
            continue

    print("[e2e] Server failed to start within 15s")
    stop_go_server()
    return False


def stop_go_server():
    """Stop the Go server."""
    global _server_process
    if _server_process:
        _server_process.send_signal(signal.SIGTERM)
        _server_process.wait(timeout=5)
        _server_process = None


def check_server_running():
    """Check if server is already running."""
    try:
        urllib.request.urlopen(f"{BASE_URL}/health", timeout=2)
        return True
    except (urllib.error.URLError, ConnectionRefusedError):
        return False


# ---------- HTTP API Tests ----------

def test_health():
    """GET /health returns healthy status."""
    resp = urllib.request.urlopen(f"{BASE_URL}/health", timeout=5)
    data = json.loads(resp.read())
    assert data["status"] == "healthy", f"expected healthy, got {data}"
    assert data["lang"] == "go", f"expected lang=go, got {data}"
    assert "uptime_s" in data
    print("[PASS] test_health")


def test_status():
    """GET /api/v1/status returns server status."""
    resp = urllib.request.urlopen(f"{BASE_URL}/api/v1/status", timeout=5)
    data = json.loads(resp.read())
    assert "source" in data, f"missing 'source' in {data}"
    assert "uptime_s" in data, f"missing 'uptime_s' in {data}"
    assert "tick" in data, f"missing 'tick' in {data}"
    print("[PASS] test_status")


def test_latest_sensing():
    """GET /api/v1/sensing/latest returns sensing data or 'no data'."""
    resp = urllib.request.urlopen(f"{BASE_URL}/api/v1/sensing/latest", timeout=5)
    data = json.loads(resp.read())
    assert isinstance(data, dict)
    # Either has sensing data or "no data yet"
    if "status" in data:
        assert "no data" in data["status"]
    else:
        assert "type" in data or "features" in data
    print("[PASS] test_latest_sensing")


def test_vital_signs():
    """GET /api/v1/vital-signs returns vitals or 'no vitals'."""
    resp = urllib.request.urlopen(f"{BASE_URL}/api/v1/vital-signs", timeout=5)
    data = json.loads(resp.read())
    assert isinstance(data, dict)
    print("[PASS] test_vital_signs")


def test_cors_headers():
    """OPTIONS request returns CORS headers."""
    req = urllib.request.Request(f"{BASE_URL}/health", method="OPTIONS")
    try:
        resp = urllib.request.urlopen(req, timeout=5)
        cors = resp.headers.get("Access-Control-Allow-Origin")
        assert cors == "*", f"expected CORS *, got {cors}"
    except urllib.error.HTTPError as e:
        cors = e.headers.get("Access-Control-Allow-Origin")
        assert cors == "*", f"expected CORS *, got {cors}"
    print("[PASS] test_cors_headers")


def test_index_page():
    """GET / returns HTML with server info."""
    resp = urllib.request.urlopen(f"{BASE_URL}/", timeout=5)
    body = resp.read().decode()
    assert "Go DensePose" in body, "index should mention Go DensePose"
    print("[PASS] test_index_page")


# ---------- WebSocket Tests ----------

def test_websocket_connection():
    """Connect to WebSocket and receive a sensing update."""
    if not HAS_WS:
        print("[SKIP] test_websocket_connection (websocket-client not installed)")
        return

    ws = websocket.create_connection(WS_URL, timeout=5)
    try:
        # Wait for a sensing_update message
        ws.settimeout(10)
        raw = ws.recv()
        data = json.loads(raw)

        assert data["type"] == "sensing_update", f"unexpected type: {data.get('type')}"
        assert "timestamp" in data
        assert "features" in data
        assert "classification" in data

        feat = data["features"]
        assert "mean_rssi" in feat or "variance" in feat, f"missing feature fields: {feat}"

        cls = data["classification"]
        assert "motion_level" in cls
        assert "presence" in cls
        assert "confidence" in cls

        print("[PASS] test_websocket_connection")
    finally:
        ws.close()


def test_websocket_sensing_update_fields():
    """Verify all expected fields in the sensing update."""
    if not HAS_WS:
        print("[SKIP] test_websocket_sensing_update_fields (websocket-client not installed)")
        return

    ws = websocket.create_connection(WS_URL, timeout=5)
    try:
        ws.settimeout(10)
        raw = ws.recv()
        data = json.loads(raw)

        required_fields = ["type", "timestamp", "source", "tick", "features", "classification"]
        for field in required_fields:
            assert field in data, f"missing field: {field}"

        # signal_field should be present when server is producing data
        if "signal_field" in data and data["signal_field"]:
            sf = data["signal_field"]
            assert "grid_size" in sf
            assert "values" in sf
            assert len(sf["values"]) == sf["grid_size"][0] * sf["grid_size"][2]

        print("[PASS] test_websocket_sensing_update_fields")
    finally:
        ws.close()


def test_websocket_multiple_updates():
    """Verify we can receive multiple sensing updates in sequence."""
    if not HAS_WS:
        print("[SKIP] test_websocket_multiple_updates (websocket-client not installed)")
        return

    ws = websocket.create_connection(WS_URL, timeout=5)
    try:
        ws.settimeout(10)
        updates = []
        for _ in range(3):
            raw = ws.recv()
            data = json.loads(raw)
            updates.append(data)

        # Ticks should be increasing
        ticks = [u["tick"] for u in updates]
        assert ticks == sorted(ticks), f"ticks not monotonic: {ticks}"

        # Timestamps should be increasing
        timestamps = [u["timestamp"] for u in updates]
        for i in range(1, len(timestamps)):
            assert timestamps[i] >= timestamps[i - 1], "timestamps not monotonic"

        print("[PASS] test_websocket_multiple_updates")
    finally:
        ws.close()


# ---------- Runner ----------

def main():
    managed = False
    if not check_server_running():
        print("[e2e] Server not running, starting it...")
        if not start_go_server():
            print("[FAIL] Could not start Go server")
            sys.exit(1)
        managed = True
        time.sleep(2)  # Let simulated data accumulate
    else:
        print(f"[e2e] Using existing server at {BASE_URL}")

    try:
        passed = 0
        failed = 0
        skipped = 0
        tests = [
            test_health,
            test_status,
            test_latest_sensing,
            test_vital_signs,
            test_cors_headers,
            test_index_page,
            test_websocket_connection,
            test_websocket_sensing_update_fields,
            test_websocket_multiple_updates,
        ]

        for test_fn in tests:
            try:
                test_fn()
                passed += 1
            except AssertionError as e:
                print(f"[FAIL] {test_fn.__name__}: {e}")
                failed += 1
            except Exception as e:
                if "SKIP" in str(e) or "websocket-client" in str(e):
                    skipped += 1
                else:
                    print(f"[FAIL] {test_fn.__name__}: {e}")
                    failed += 1

        print(f"\n{'='*50}")
        print(f"Results: {passed} passed, {failed} failed, {skipped} skipped")
        print(f"{'='*50}")

        if failed > 0:
            sys.exit(1)
    finally:
        if managed:
            stop_go_server()


if __name__ == "__main__":
    main()
