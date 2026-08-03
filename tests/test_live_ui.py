import sys
import time
from playwright.sync_api import sync_playwright

def test_live_ui():
    url = "http://192.168.50.189:50056/ui/"
    print(f"🚀 Launching Chromium E2E Test against {url}...")

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        context = browser.new_context(http_credentials={"username": "admin", "password": "blackhole"})
        page = context.new_page()

        # Step 1: Open UI
        page.goto(url)
        page.wait_for_load_state("domcontentloaded")
        print("✅ Page loaded successfully. Title:", page.title())

        # Step 2: Test Tab Switch to Photo Album
        print("🖱️ Clicking '#tabAlbum' (照片相册)...")
        page.click("#tabAlbum")
        time.sleep(1.5)

        album_display = page.eval_on_selector("#albumView", "el => getComputedStyle(el).display")
        assert album_display != "none", f"Expected #albumView to be visible, got {album_display}"
        print("✅ #albumView is now VISIBLE (display: block)")

        # Step 3: Verify Photo Grid Items
        photo_cards = page.query_selector_all(".photo-card")
        print(f"📸 Rendered photo cards count: {len(photo_cards)}")
        assert len(photo_cards) > 0, "Expected at least 1 photo card in album view!"

        # Step 4: Verify Pagination Bar & Select Box
        select_val = page.eval_on_selector("#pageSizeSelect", "el => el.value")
        print(f"📄 Photo Album page size selector value: {select_val}")
        assert select_val == "25", f"Expected default page size 25, got {select_val}"

        page_info = page.text_content("#pageInfoLabel")
        print(f"ℹ️ Album page info text: '{page_info.strip()}'")

        # Take screenshot of Album View
        screenshot_album = "/home/qtopierw/.gemini/antigravity-cli/brain/76330106-88cd-4bdb-95ac-c7d42fc790c5/live_album_view.png"
        page.screenshot(path=screenshot_album)
        print(f"📷 Saved live album screenshot to: {screenshot_album}")

        # Step 5: Test Dark Filter Toggle
        print("🖱️ Clicking '#darkFilterBtn' (纯黑误拍废片)...")
        page.click("#darkFilterBtn")
        time.sleep(1.5)
        dark_cards = page.query_selector_all(".photo-card")
        print(f"🖤 Dark filter result cards count: {len(dark_cards)}")

        # Step 6: Test Tab Switch back to File Manager
        print("🖱️ Clicking '#tabFiles' (文件管理)...")
        page.click("#tabFiles")
        time.sleep(1)

        files_display = page.eval_on_selector("#filesView", "el => getComputedStyle(el).display")
        assert files_display != "none", f"Expected #filesView to be visible, got {files_display}"
        print("✅ #filesView is now VISIBLE")

        file_select_val = page.eval_on_selector("#filePageSizeSelect", "el => el.value")
        print(f"📁 File Manager page size selector value: {file_select_val}")
        assert file_select_val == "25", f"Expected file manager page size 25, got {file_select_val}"

        # Take screenshot of File Manager View
        screenshot_files = "/home/qtopierw/.gemini/antigravity-cli/brain/76330106-88cd-4bdb-95ac-c7d42fc790c5/live_files_view.png"
        page.screenshot(path=screenshot_files)
        print(f"📷 Saved live files screenshot to: {screenshot_files}")

        browser.close()
        print("\n🎉 ALL E2E AUTOMATED UI TESTS PASSED CLEANLY!")

if __name__ == "__main__":
    test_live_ui()
