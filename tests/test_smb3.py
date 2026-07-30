#!/usr/bin/env python3
"""
SMB3 Protocol Handshake Test using Python's smbprotocol library
"""

import smbprotocol.connection
import smbprotocol.session
import smbprotocol.tree

import uuid

TARGET_IP = "192.168.50.189"
PORT = 445

def test_smb3_connection():
    print(f"👉 Testing SMB3 Protocol Handshake using smbprotocol library ({TARGET_IP}:{PORT})...")
    conn = smbprotocol.connection.Connection(guid=uuid.uuid4(), server_name=TARGET_IP, port=PORT)
    try:
        conn.connect(timeout=5)
        print(f"✅ SMB3 Server Connection Established!")
        print(f"   - Selected SMB Dialect: {hex(conn.dialect)}")
        print(f"   - Max Read Size: {conn.max_read_size} bytes")
        print(f"   - Max Write Size: {conn.max_write_size} bytes")
        conn.disconnect()
        return True
    except Exception as e:
        print(f"❌ SMB3 Connection Error: {e}")
        return False

if __name__ == "__main__":
    test_smb3_connection()
