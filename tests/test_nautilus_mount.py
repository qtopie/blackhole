#!/usr/bin/env python3
import os
import sys
import gi
gi.require_version('Gio', '2.0')
from gi.repository import Gio, GLib

TARGET_IP = os.getenv("TARGET_IP", "192.168.50.189")
SMB_PORT = os.getenv("SMB_PORT", "")

if SMB_PORT:
    URI = f"smb://{TARGET_IP}:{SMB_PORT}/share"
else:
    URI = f"smb://{TARGET_IP}/share"

USERNAME = "admin"
PASSWORD = "blackhole"
DOMAIN = "WORKGROUP"

def mount_smb(uri, username, password, domain="WORKGROUP"):
    print(f"👉 正在向 Nautilus (GVFS) 发起 SMB 挂载请求: {uri} ...")
    file = Gio.File.new_for_uri(uri)
    
    def on_mount_done(source_object, res, user_data):
        try:
            success = source_object.mount_enclosing_volume_finish(res)
            print("✅ SMB 挂载成功！Nautilus 界面已同步更新。")
        except Exception as e:
            print(f"❌ 挂载失败: {e}")
        loop.quit()

    def ask_password_cb(op, message, default_user, default_domain, flags):
        print(f"🔑 捕获 Nautilus 身份验证回调, 自动填充凭据: user={username}, domain={domain}")
        op.set_username(username)
        op.set_domain(domain)
        op.set_password(password)
        op.reply(Gio.MountOperationResult.HANDLED)

    mount_op = Gio.MountOperation()
    mount_op.connect("ask-password", ask_password_cb)

    file.mount_enclosing_volume(Gio.MountMountFlags.NONE, mount_op, None, on_mount_done, None)

if __name__ == "__main__":
    loop = GLib.MainLoop()
    mount_smb(URI, USERNAME, PASSWORD, DOMAIN)
    GLib.timeout_add_seconds(5, loop.quit)
    loop.run()
