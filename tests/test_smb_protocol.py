#!/usr/bin/env python3
"""
Raw SMB2 Negotiate & Session Setup Packet Test in pure Python
"""

import socket
import struct

TARGET_IP = "192.168.50.189"
SMB_PORT = 445

def test_smb2_raw_handshake():
    print(f"👉 Connecting to SMB2 Server on {TARGET_IP}:{SMB_PORT} via Raw Socket...")
    
    try:
        s = socket.create_connection((TARGET_IP, SMB_PORT), timeout=5)
    except Exception as e:
        print(f"❌ Connection failed: {e}")
        return False

    # Construct SMB2 Negotiate Request Header (64 bytes)
    # Header: ProtocolId "\xfeSMB", StructSize 64, CreditCharge 0, Status 0, Command 0x0000 (Negotiate), CreditReq 1, Flags 0, NextCmd 0, MsgID 0, ProcID 0, TreeID 0, SessID 0, Signature 16x0
    protocol_id = b"\xfeSMB"
    struct_size = 64
    credit_charge = 0
    status = 0
    command = 0x0000 # SMB2_NEGOTIATE
    credit_req = 1
    flags = 0
    next_cmd = 0
    msg_id = 0
    proc_id = 0
    tree_id = 0
    session_id = 0
    signature = b"\x00" * 16

    header = struct.pack(
        "<4sHHIIIIQQIIQ16s",
        protocol_id,
        struct_size,
        credit_charge,
        status,
        command,
        credit_req,
        flags,
        next_cmd,
        msg_id,
        proc_id,
        tree_id,
        session_id,
        signature
    )

    # SMB2 Negotiate Request Body (36 bytes)
    # StructSize 36, DialectCount 1, SecurityMode 0, Reserved 0, Capabilities 0, ClientGUID 16x0, NegotiateContextOffset 0, NegotiateContextCount 0, Reserved2 0, Dialect 0x0210 (SMB 2.1)
    body = struct.pack(
        "<HHHII16sHHH",
        36,     # StructSize
        1,      # DialectCount
        0,      # SecurityMode
        0,      # Reserved
        0,      # Capabilities
        b"\x00" * 16, # ClientGUID
        0,      # ContextOffset
        0,      # ContextCount
        0       # Reserved2
    ) + struct.pack("<H", 0x0210) # SMB 2.1

    payload = header + body
    
    # NetBIOS Session Service Header (4 bytes: 0x00 + 3 bytes length)
    netbios_hdr = struct.pack(">I", len(payload))
    
    # Send packet
    s.sendall(netbios_hdr + payload)
    print("   [1/2] Sent SMB2 Negotiate Protocol Request.")

    # Read response NetBIOS Header
    resp_netbios = s.recv(4)
    if not resp_netbios or len(resp_netbios) < 4:
        print("❌ No response received from server.")
        s.close()
        return False

    resp_len = struct.unpack(">I", resp_netbios)[0] & 0xFFFFFF
    print(f"   [2/2] Received NetBIOS Response (Payload Size: {resp_len} bytes).")

    # Read SMB2 Response Header + Body
    resp_payload = s.recv(resp_len)
    s.close()

    if len(resp_payload) >= 64 and resp_payload[:4] == b"\xfeSMB":
        resp_command, resp_status = struct.unpack("<HI", resp_payload[12:18])
        print(f"✅ SMB2 Server Response Verified! Command: {hex(resp_command)}, Status: {hex(resp_status)}")
        return True
    else:
        print("❌ Invalid SMB2 Header received.")
        return False

if __name__ == "__main__":
    test_smb2_raw_handshake()
