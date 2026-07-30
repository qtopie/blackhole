# SMB Regression & Protocol Test Cases

This document details regression test cases for validating the native SMB2/SMB3 protocol server implementation in **Blackhole NAS**.

## Test Case 1: Protocol Negotiation & Session Setup (`NT_STATUS_INVALID_NETWORK_RESPONSE` Prevention)

### Objective
Verify that `smbclient` and standard SMB clients can negotiate SMB2/SMB3 dialects and complete session setup without triggering `NT_STATUS_INVALID_NETWORK_RESPONSE` or `NT_STATUS_INVALID_PARAMETER` errors.

### Background & Specifications
According to **[MS-SMB2] Section 2.2.4 (SMB2 NEGOTIATE Response)** and **Section 2.2.6 (SMB2 SESSION_SETUP Response)**:
1. `SMB2_NEGOTIATE` response must be exactly **65 bytes** containing valid struct fields:
   - `StructureSize`: `65` (uint16)
   - `SecurityMode`: `0` (uint16)
   - `DialectRevision`: `0x0210` (SMB 2.10) or `0x0302` (SMB 3.0.2)
   - `MaxTransactSize`, `MaxReadSize`, `MaxWriteSize`: `65536` bytes (64KB)
2. `SMB2_SESSION_SETUP` response must be **9 bytes**:
   - `StructureSize`: `9` (uint16)
   - `SessionFlags`: `1` (`SMB2_SESSION_FLAG_IS_GUEST`)

### Execution Commands

```bash
# 1. Test using smbclient CLI
smbclient -L 192.168.50.189 -U admin%blackhole

# 2. Test using Python smbprotocol library
python3 tests/test_smb3.py

# 3. Test using raw binary packet inspector
python3 tests/test_smb_protocol.py
```

### Expected Output

`smbclient` output:
```text
	Sharename       Type      Comment
	---------       ----      -------
```

`test_smb3.py` output:
```text
👉 Testing SMB3 Protocol Handshake using smbprotocol library (192.168.50.189:445)...
✅ SMB3 Server Connection Established!
   - Selected SMB Dialect: 0x302
```

`test_smb_protocol.py` output:
```text
👉 Connecting to SMB2 Server on 192.168.50.189:445 via Raw Socket...
   [1/2] Sent SMB2 Negotiate Protocol Request.
   [2/2] Received NetBIOS Response (Payload Size: 129 bytes).
✅ SMB2 Server Response Verified! Command: 0x0, Status: 0x0
```

---

## Test Case 2: Multi-Protocol File Operations via SMB3

### Objective
Ensure file creation (`CommandCreate`), data write (`CommandWrite`), and closure (`CommandClose`) operate cleanly over SMB3 connections.

### Execution Command

```bash
python3 tests/test_blackhole.py
```

### Verification Criteria
- TCP Port `445` opens and responds to incoming connections.
- Connection does not drop during data payload transfers.

---

## Test Case 3: SMB2 Directory Listing (`ls`) & Disk Attribute Query (`dskattr`)

### Objective
Verify that `smbclient` can cleanly enumerate directory entries (`CommandQueryDirectory`) using handle-based cursor state tracking, and query total/available disk allocation units (`CommandQueryInfo`) without raising `Error in dskattr: NT_STATUS_INVALID_NETWORK_RESPONSE` or looping infinitely.

### Background & Specifications
1. **Handle State Tracking**:
   Each `CommandCreate` request returns a unique `FileId` handle. The server tracks `handleState.cursor` per handle so subsequent `SMB2_QUERY_DIRECTORY` calls return batch entries and terminate with `STATUS_NO_MORE_FILES` (`0x80000006`).
2. **Disk Space Query (`FileFsSizeInformation`)**:
   Upon completing directory listing, `smbclient` issues `SMB2_QUERY_INFO` (`Command 0x0010`) with `InfoType = 0x02` (Filesystem) and `FsInfoClass = 0x03` (`FileFsSizeInformation`). The response must include a 24-byte payload containing:
   - `TotalAllocationUnits`: `int64`
   - `AvailableAllocationUnits`: `int64`
   - `SectorsPerAllocationUnit`: `uint32`
   - `BytesPerSector`: `uint32`

### Execution Command

```bash
smbclient //192.168.50.189/share -U admin%blackhole -c "ls"
```

### Expected Output

```text
  python_test.txt                     A       57  Thu Jan  1 08:00:00 1970
  test_upload.txt                     A       81  Thu Jan  1 08:00:00 1970
  vault                               D        0  Thu Jan  1 08:00:00 1970

		100000000 blocks of size 4096. 80000000 blocks available
```

### Verification Criteria
- `smbclient` prints directory contents and disk block capacity cleanly.
- Command exits with return status code `0`.
- No `Error in dskattr: NT_STATUS_INVALID_NETWORK_RESPONSE` or infinite loop warning is printed.

---

## Test Case 4: GNOME Nautilus / GVFS Desktop Mount Integration (`PyGObject`)

### Objective
Validate that desktop file managers (GNOME Nautilus, Linux File Manager via GVFS `libsmbclient`) can asynchronously mount the SMB share (`smb://<ip>/share`) using PyGObject (`Gio.File.mount_enclosing_volume`).

### Background & Specifications
GVFS initiates a full SMB session flow:
1. `SMB2_NEGOTIATE` & `SMB2_SESSION_SETUP` with SPNEGO/NTLMSSP authentication challenge.
2. `SMB2_TREE_CONNECT` to `\\<ip>\share`.
3. `SMB2_CREATE` & `SMB2_QUERY_INFO` to retrieve `FileFsVolumeInformation` and root attributes.
4. `ask-password` callback handling for domain `WORKGROUP` and username/password credentials.

### Execution Command

```bash
# Executable PyGObject test script
python3 tests/test_nautilus_mount.py
```

### Target Output

```text
👉 正在向 Nautilus (GVFS) 发起 SMB 挂载请求: smb://192.168.50.189/share ...
🔑 捕获 Nautilus 身份验证回调, 自动填充凭据: user=admin, domain=WORKGROUP
✅ SMB 挂载成功！Nautilus 界面已同步更新。
```

### Verification Criteria
- The share is mounted into GVFS (`/run/user/1000/gvfs/smb-share:server=192.168.50.189,share=share`).
- Desktop File Manager (Nautilus) shows the mounted network volume under sidebar.


