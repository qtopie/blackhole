#!/usr/bin/env python3
"""
NFSv4 Protocol Handshake and Procedure Verification
Tests RPC NULL probe and COMPOUND [SETCLIENTID, PUTROOTFH, GETATTR] against Blackhole NFS server.
"""

import socket
import struct
import sys

TARGET_IP = "127.0.0.1"
PORT = 2049

if len(sys.argv) > 1:
    TARGET_IP = sys.argv[1]
if len(sys.argv) > 2:
    PORT = int(sys.argv[2])

def encode_record(data: bytes) -> bytes:
    # RFC 5531: 4-byte header, highest bit set (0x80000000 | length)
    length = len(data)
    hdr = struct.pack(">I", 0x80000000 | length)
    return hdr + data

def read_record(sock: socket.socket) -> bytes:
    hdr = sock.recv(4)
    if len(hdr) < 4:
        raise RuntimeError("Failed to read record marker header")
    h = struct.unpack(">I", hdr)[0]
    length = h & 0x7FFFFFFF
    buf = bytearray()
    while len(buf) < length:
        chunk = sock.recv(length - len(buf))
        if not chunk:
            break
        buf.extend(chunk)
    return bytes(buf)

def xdr_pad(length: int) -> int:
    return (4 - (length % 4)) % 4

def encode_string(s: str) -> bytes:
    b = s.encode("utf-8")
    pad = b"\x00" * xdr_pad(len(b))
    return struct.pack(">I", len(b)) + b + pad

def test_nfs4_handshake():
    print(f"👉 Testing NFSv4 Protocol Server at {TARGET_IP}:{PORT}...")
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(5.0)
    try:
        sock.connect((TARGET_IP, PORT))
    except Exception as e:
        print(f"❌ Connection failed: {e}")
        return False

    # 1. Test RPC NULL (Program 100003, Vers 4, Proc 0)
    print("  [1/2] Sending RPC NULL probe (proc 0)...")
    rpc_null = struct.pack(
        ">IIIIII II II",
        0x12345678, # XID
        0,          # CALL
        2,          # RPC Vers 2
        100003,     # Program NFS
        4,          # Version 4
        0,          # Proc NULL
        0, 0,       # Cred (AUTH_NONE)
        0, 0,       # Verf (AUTH_NONE)
    )
    sock.sendall(encode_record(rpc_null))
    reply = read_record(sock)
    xid, mtype, reply_stat, vflav, vlen, accept_stat = struct.unpack(">IIIIII", reply[:24])
    assert xid == 0x12345678, f"XID mismatch: {hex(xid)}"
    assert reply_stat == 0, f"Reply not accepted: {reply_stat}"
    assert accept_stat == 0, f"Accept stat not success: {accept_stat}"
    print("  ✅ RPC NULL probe PASSED!")

    # 2. Test COMPOUND [PUTROOTFH, GETFH, GETATTR] (Program 100003, Vers 4, Proc 1)
    print("  [2/2] Sending COMPOUND [PUTROOTFH, GETFH, GETATTR] (proc 1)...")
    compound_body = bytearray()
    compound_body.extend(encode_string("py-nfs-test")) # tag
    compound_body.extend(struct.pack(">II", 0, 3))     # minorver=0, 3 ops

    # Op 1: PUTROOTFH (24)
    compound_body.extend(struct.pack(">I", 24))

    # Op 2: GETFH (10)
    compound_body.extend(struct.pack(">I", 10))

    # Op 3: GETATTR (9) requesting FATTR4_TYPE (bit 1) and FATTR4_LEASE_TIME (bit 10) -> (1<<1 | 1<<10) = 2 | 1024 = 1026
    compound_body.extend(struct.pack(">I", 9))
    compound_body.extend(struct.pack(">II", 1, 1026)) # bitmap count=1, mask=1026

    rpc_compound = struct.pack(
        ">IIIIII II II",
        0x12345679, # XID
        0,          # CALL
        2,          # RPC Vers 2
        100003,     # Program NFS
        4,          # Version 4
        1,          # Proc COMPOUND
        0, 0,       # Cred (AUTH_NONE)
        0, 0,       # Verf (AUTH_NONE)
    ) + bytes(compound_body)

    sock.sendall(encode_record(rpc_compound))
    reply2 = read_record(sock)
    xid, mtype, reply_stat, vflav, vlen, accept_stat = struct.unpack(">IIIIII", reply2[:24])
    assert xid == 0x12345679, f"XID mismatch: {hex(xid)}"
    assert accept_stat == 0, f"Accept stat error: {accept_stat}"

    # Parse COMPOUND response
    cr = reply2[24:]
    comp_status = struct.unpack(">I", cr[:4])[0]
    assert comp_status == 0, f"Compound status error: {comp_status}"
    print(f"  ✅ COMPOUND [PUTROOTFH, GETFH, GETATTR] handshake succeeded (Status: NFS4_OK)")
    sock.close()
    return True

if __name__ == "__main__":
    ok = test_nfs4_handshake()
    sys.exit(0 if ok else 1)
