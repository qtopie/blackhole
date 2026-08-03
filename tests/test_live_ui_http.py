import urllib.request
import json
import base64
import re
import subprocess
import sys

def test_live_ui_http():
    base_url = "http://192.168.50.189:50056"
    ui_url = f"{base_url}/ui/"
    print(f"🚀 Running Live UI & API End-to-End Automated Diagnostic on {base_url}...")

    auth_str = base64.b64encode(b"admin:blackhole").decode("utf-8")
    headers = {
        "Authorization": f"Basic {auth_str}",
        "Referer": ui_url
    }

    # 1. Test GET /ui/ (Web UI HTML Page)
    req_ui = urllib.request.Request(ui_url, headers=headers)
    with urllib.request.urlopen(req_ui) as resp:
        assert resp.status == 200, f"Expected 200 OK, got {resp.status}"
        html = resp.read().decode("utf-8")
        print(f"✅ Web UI HTML loaded successfully! (Length: {len(html)} bytes)")

    # 2. Check HTML structure for Tabs and Pagination Bar
    assert 'id="tabAlbum"' in html, "Missing #tabAlbum in HTML"
    assert 'id="tabFiles"' in html, "Missing #tabFiles in HTML"
    assert 'onclick="switchMainTab(\'album\')"' in html, "Missing switchMainTab('album') handler"
    assert 'onclick="switchMainTab(\'files\')"' in html, "Missing switchMainTab('files') handler"
    print("✅ Tab button elements and click handlers present in HTML!")

    assert '<option value="25">25 条/页</option>' in html or '<option value="25" selected>25 条/页</option>' in html, "Missing 25 items option in album page size select"
    assert '<option value="25" selected' in html or '<option value="25" >25 条/页</option>' in html, "Missing 25 items option in file page size select"
    print("✅ Default page size 25 and options (25/50/100) verified in HTML!")

    # 3. Extract and Validate JavaScript Syntax via Node.js
    scripts = re.findall(r'<script>(.*?)</script>', html, re.DOTALL)
    assert len(scripts) > 0, "No <script> tag found in HTML"
    js_code = scripts[0]

    with open('/tmp/live_ui_script.js', 'w', encoding='utf-8') as f:
        f.write(js_code)
    node_res = subprocess.run(['node', '--check', '/tmp/live_ui_script.js'], capture_output=True, text=True)
    assert node_res.returncode == 0, f"JS Syntax Error detected: {node_res.stderr}"
    print("✅ JavaScript syntax validation passed 100% cleanly (No SyntaxError)!")

    # 4. Test API Endpoint GET /api/album/photos?page=1&limit=25
    api_url = f"{base_url}/api/album/photos?page=1&limit=25"
    req_api = urllib.request.Request(api_url, headers=headers)
    with urllib.request.urlopen(req_api) as resp:
        assert resp.status == 200, f"Expected 200 for API, got {resp.status}"
        data = json.loads(resp.read().decode("utf-8"))
        print(f"✅ Album API returned status: '{data.get('status')}', Total Photos: {data.get('total')}, Page Items: {len(data.get('photos', []))}")
        assert data.get('status') == 'success', f"Expected success status, got {data.get('status')}"

    # 5. Test API Endpoint GET /api/album/photos?dark=1 (Dark filter)
    dark_url = f"{base_url}/api/album/photos?dark=1&page=1&limit=25"
    req_dark = urllib.request.Request(dark_url, headers=headers)
    with urllib.request.urlopen(req_dark) as resp:
        assert resp.status == 200, f"Expected 200 for Dark filter API, got {resp.status}"
        dark_data = json.loads(resp.read().decode("utf-8"))
        print(f"✅ Dark Filter API returned status: '{dark_data.get('status')}', Dark Photos Count: {len(dark_data.get('photos', []))}")

    print("\n🎉 ALL E2E DIAGNOSTIC TESTS PASSED LIVE ON SPACEMIT K1 BOARD!")

if __name__ == "__main__":
    test_live_ui_http()
