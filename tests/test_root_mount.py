#!/usr/bin/env python3
import os
import gi
gi.require_version('Gio', '2.0')
from gi.repository import Gio, GLib

TARGET_IP = os.getenv("TARGET_IP", "192.168.50.189")

# Test 1: Root server enumeration (Nautilus auto-discovery path)
ROOT_URI = f"smb://{TARGET_IP}/"
# Test 2: Direct share mount (explicit path)
SHARE_URI = f"smb://{TARGET_IP}/share"

USERNAME = "admin"
PASSWORD = "blackhole"
DOMAIN = "WORKGROUP"

def run_test(uri, label, loop):
    print(f"\n👉 [{label}] 正在测试: {uri} ...")
    file = Gio.File.new_for_uri(uri)

    def on_mount_done(source_object, res, user_data):
        try:
            source_object.mount_enclosing_volume_finish(res)
            print(f"✅ [{label}] 成功!")
        except Exception as e:
            print(f"❌ [{label}] 失败: {e}")
        loop.quit()

    def ask_password_cb(op, message, default_user, default_domain, flags):
        print(f"   🔑 自动填充凭据: user={USERNAME}, domain={DOMAIN}")
        op.set_username(USERNAME)
        op.set_domain(DOMAIN)
        op.set_password(PASSWORD)
        op.reply(Gio.MountOperationResult.HANDLED)

    mount_op = Gio.MountOperation()
    mount_op.connect("ask-password", ask_password_cb)
    file.mount_enclosing_volume(Gio.MountMountFlags.NONE, mount_op, None, on_mount_done, None)

if __name__ == "__main__":
    import sys
    test_uri = sys.argv[1] if len(sys.argv) > 1 else ROOT_URI

    loop = GLib.MainLoop()
    run_test(test_uri, "SMB Mount", loop)
    GLib.timeout_add_seconds(5, loop.quit)
    loop.run()
