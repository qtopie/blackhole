package nfs

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/qtopie/blackhole/internal/config"
)

// setupTestNFSServer boots a test NFSv4 server on a free loopback port.
func setupTestNFSServer(t *testing.T) (*Server, string, string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "blackhole-nfs-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create test directory tree
	if err := os.MkdirAll(filepath.Join(tmpDir, "public"), 0755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}
	helloPath := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(helloPath, []byte("Hello NFSv4 World!"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Pick a free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()

	cfg := &config.Config{
		ShareDir: tmpDir,
		NFSPort:  port,
	}

	srv := NewServer(cfg)
	go func() {
		_ = srv.Start()
	}()

	addr := net.JoinHostPort("127.0.0.1", port)
	// Wait until listening
	for i := 0; i < 50; i++ {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cleanup := func() {
		_ = srv.Shutdown(context.Background())
		_ = os.RemoveAll(tmpDir)
	}

	return srv, addr, tmpDir, cleanup
}

func sendRPC(conn net.Conn, xid uint32, proc uint32, body []byte) ([]byte, error) {
	w := NewXDRWriter()
	w.WriteUint32(xid)
	w.WriteUint32(RPCMsgCall)
	w.WriteUint32(2)              // rpcvers
	w.WriteUint32(RPCProgramNFS)  // prog = 100003
	w.WriteUint32(RPCVersion4)    // vers = 4
	w.WriteUint32(proc)
	// cred: AUTH_NONE
	w.WriteUint32(AuthFlavorNone)
	w.WriteBytes(nil)
	// verf: AUTH_NONE
	w.WriteUint32(AuthFlavorNone)
	w.WriteBytes(nil)

	if len(body) > 0 {
		w.WriteRaw(body)
	}

	if err := WriteRecord(conn, w.Bytes()); err != nil {
		return nil, err
	}

	resp, err := ReadRecord(conn)
	if err != nil {
		return nil, err
	}

	r := NewXDRReader(resp)
	rxid, _ := r.ReadUint32()
	if rxid != xid {
		return nil, fmt.Errorf("xid mismatch: got %d, expected %d", rxid, xid)
	}
	mtype, _ := r.ReadUint32()
	if mtype != RPCMsgReply {
		return nil, fmt.Errorf("expected reply, got %d", mtype)
	}
	replyStat, _ := r.ReadUint32()
	if replyStat != RPCMsgAccepted {
		return nil, fmt.Errorf("reply denied: %d", replyStat)
	}
	// verf
	_, _ = r.ReadUint32()
	_, _ = r.ReadBytes()
	acceptStat, _ := r.ReadUint32()
	if acceptStat != RPCAcceptSuccess {
		return nil, fmt.Errorf("accept failed: %d", acceptStat)
	}

	return resp[r.off:], nil
}

// Scenario 1: [SPEC-NFS-001] RPC NULL 连通性探测
func TestNFS_RPC_Null(t *testing.T) {
	_, addr, _, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	payload, err := sendRPC(conn, 1001, RPCProcNull, nil)
	if err != nil {
		t.Fatalf("RPC NULL failed: %v", err)
	}
	if len(payload) != 0 {
		t.Fatalf("expected empty payload for RPC NULL, got %d bytes", len(payload))
	}
}

// Scenario 2: [SPEC-NFS-002] 客户端租约初始化与防重放 (SETCLIENTID + CONFIRM)
func TestNFS_SetClientID_And_Confirm(t *testing.T) {
	_, addr, _, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. Build OP_SETCLIENTID COMPOUND
	cw := NewXDRWriter()
	cw.WriteString("setclientid-test")
	cw.WriteUint32(0) // minorversion = 0
	cw.WriteUint32(1) // 1 operation
	// OpSetclientid
	cw.WriteUint32(OpSetclientid)
	cw.WriteFixedBytes([]byte("VERIFIER")) // 8-byte verifier
	cw.WriteString("Linux-Client-Test-Host") // client identifier
	// callback
	cw.WriteUint32(0)
	cw.WriteString("tcp")
	cw.WriteString("127.0.0.1.8.1")
	cw.WriteUint32(1)

	resp, err := sendRPC(conn, 1002, RPCProcCompound, cw.Bytes())
	if err != nil {
		t.Fatalf("SETCLIENTID compound failed: %v", err)
	}

	cr := NewXDRReader(resp)
	status, _ := cr.ReadUint32()
	if status != NFS4OK {
		t.Fatalf("expected compound status NFS4OK, got %d", status)
	}
	tag, _ := cr.ReadString()
	if tag != "setclientid-test" {
		t.Fatalf("unexpected tag: %s", tag)
	}
	resCount, _ := cr.ReadUint32()
	if resCount != 1 {
		t.Fatalf("expected 1 result, got %d", resCount)
	}

	opNum, _ := cr.ReadUint32()
	if opNum != OpSetclientid {
		t.Fatalf("expected op OpSetclientid, got %d", opNum)
	}
	opStatus, _ := cr.ReadUint32()
	if opStatus != NFS4OK {
		t.Fatalf("expected OpSetclientid status NFS4OK, got %d", opStatus)
	}
	clientID, _ := cr.ReadUint64()
	confirmVerf, _ := cr.ReadFixedBytes(8)
	if clientID == 0 {
		t.Fatalf("expected non-zero clientID")
	}

	// 2. Call OP_SETCLIENTID_CONFIRM
	cw2 := NewXDRWriter()
	cw2.WriteString("confirm-test")
	cw2.WriteUint32(0)
	cw2.WriteUint32(1)
	cw2.WriteUint32(OpSetclientidConf)
	cw2.WriteUint64(clientID)
	cw2.WriteFixedBytes(confirmVerf)

	resp2, err := sendRPC(conn, 1003, RPCProcCompound, cw2.Bytes())
	if err != nil {
		t.Fatalf("SETCLIENTID_CONFIRM failed: %v", err)
	}
	cr2 := NewXDRReader(resp2)
	status2, _ := cr2.ReadUint32()
	if status2 != NFS4OK {
		t.Fatalf("expected confirm compound status NFS4OK, got %d", status2)
	}
}

// Scenario 3: [SPEC-NFS-003] 根文件句柄与文件系统属性获取 (PUTROOTFH + GETATTR)
func TestNFS_PutRootFH_GetAttr(t *testing.T) {
	_, addr, _, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	cw := NewXDRWriter()
	cw.WriteString("root-attr-test")
	cw.WriteUint32(0) // minorversion
	cw.WriteUint32(3) // 3 operations: PUTROOTFH, GETFH, GETATTR

	// 1. PUTROOTFH
	cw.WriteUint32(OpPutrootfh)

	// 2. GETFH
	cw.WriteUint32(OpGetfh)

	// 3. GETATTR (request FATTR4_TYPE, FATTR4_FSID, FATTR4_LEASE_TIME)
	cw.WriteUint32(OpGetattr)
	cw.WriteBitmap([]uint32{Fattr4Type | Fattr4Fsid | Fattr4LeaseTime, 0})

	resp, err := sendRPC(conn, 1004, RPCProcCompound, cw.Bytes())
	if err != nil {
		t.Fatalf("PUTROOTFH+GETATTR failed: %v", err)
	}

	cr := NewXDRReader(resp)
	status, _ := cr.ReadUint32()
	if status != NFS4OK {
		t.Fatalf("compound status error: %d", status)
	}
	_, _ = cr.ReadString()
	resCount, _ := cr.ReadUint32()
	if resCount != 3 {
		t.Fatalf("expected 3 results, got %d", resCount)
	}

	// Res 1: PUTROOTFH
	_ = mustReadOpStatus(t, cr, OpPutrootfh)

	// Res 2: GETFH
	_ = mustReadOpStatus(t, cr, OpGetfh)
	rootFH, err := cr.ReadBytes()
	if err != nil || len(rootFH) == 0 {
		t.Fatalf("failed to read rootFH: %v", err)
	}

	// Res 3: GETATTR
	_ = mustReadOpStatus(t, cr, OpGetattr)
	respBm, err := cr.ReadBitmap()
	if err != nil {
		t.Fatalf("failed to read attr bitmap: %v", err)
	}
	if len(respBm) < 1 || (respBm[0]&Fattr4Type) == 0 {
		t.Fatalf("expected Fattr4Type in response bitmap")
	}

	attrVals, err := cr.ReadBytes()
	if err != nil {
		t.Fatalf("failed to read attrVals: %v", err)
	}
	valReader := NewXDRReader(attrVals)
	fType, _ := valReader.ReadUint32()
	if fType != NF4DIR {
		t.Fatalf("expected root type NF4DIR (%d), got %d", NF4DIR, fType)
	}
}

// Scenario 4: [SPEC-NFS-004] 目录枚举与文件名解析 (LOOKUP + READDIR)
func TestNFS_Lookup_And_ReadDir(t *testing.T) {
	_, addr, _, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. Test LOOKUP("public")
	cw := NewXDRWriter()
	cw.WriteString("lookup-test")
	cw.WriteUint32(0)
	cw.WriteUint32(3)
	cw.WriteUint32(OpPutrootfh)
	cw.WriteUint32(OpLookup)
	cw.WriteString("public")
	cw.WriteUint32(OpGetfh)

	resp, err := sendRPC(conn, 1005, RPCProcCompound, cw.Bytes())
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	cr := NewXDRReader(resp)
	if st, _ := cr.ReadUint32(); st != NFS4OK {
		t.Fatalf("lookup compound status error: %d", st)
	}
	_, _ = cr.ReadString()
	_, _ = cr.ReadUint32() // resCount
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpLookup)
	mustReadOpStatus(t, cr, OpGetfh)
	pubFH, _ := cr.ReadBytes()
	if len(pubFH) == 0 {
		t.Fatalf("expected non-empty public directory FH")
	}

	// 2. Test READDIR on root
	cw2 := NewXDRWriter()
	cw2.WriteString("readdir-test")
	cw2.WriteUint32(0)
	cw2.WriteUint32(2)
	cw2.WriteUint32(OpPutrootfh)
	cw2.WriteUint32(OpReaddir)
	cw2.WriteUint64(0)                    // cookie
	cw2.WriteFixedBytes([]byte("COOKVERF")) // cookieverf
	cw2.WriteUint32(1024)                 // dircount
	cw2.WriteUint32(4096)                 // maxcount
	cw2.WriteBitmap([]uint32{Fattr4Type, 0})

	resp2, err := sendRPC(conn, 1006, RPCProcCompound, cw2.Bytes())
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	cr2 := NewXDRReader(resp2)
	if st, _ := cr2.ReadUint32(); st != NFS4OK {
		t.Fatalf("readdir compound status error: %d", st)
	}
	_, _ = cr2.ReadString()
	_, _ = cr2.ReadUint32()
	mustReadOpStatus(t, cr2, OpPutrootfh)
	mustReadOpStatus(t, cr2, OpReaddir)
	_, _ = cr2.ReadFixedBytes(8) // cookieverf

	foundHello := false
	foundPublic := false
	for {
		hasNext, _ := cr2.ReadUint32()
		if hasNext == 0 {
			break
		}
		_, _ = cr2.ReadUint64() // cookie
		name, _ := cr2.ReadString()
		if name == "hello.txt" {
			foundHello = true
		}
		if name == "public" {
			foundPublic = true
		}
		// skip attrs
		_, _ = cr2.ReadBitmap()
		_, _ = cr2.ReadBytes()
	}

	if !foundHello || !foundPublic {
		t.Fatalf("readdir entries missing: foundHello=%v, foundPublic=%v", foundHello, foundPublic)
	}
}

// Scenario 5: [SPEC-NFS-005] 文件只读与写入 I/O (OPEN + READ + WRITE + CLOSE)
func TestNFS_File_Read_Write(t *testing.T) {
	_, addr, _, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. OPEN "hello.txt" and READ
	cw := NewXDRWriter()
	cw.WriteString("open-read-test")
	cw.WriteUint32(0)
	cw.WriteUint32(3)
	cw.WriteUint32(OpPutrootfh)
	cw.WriteUint32(OpLookup)
	cw.WriteString("hello.txt")
	cw.WriteUint32(OpRead)
	cw.WriteFixedBytes(make([]byte, 16)) // stateid
	cw.WriteUint64(0)                    // offset
	cw.WriteUint32(1024)                 // count

	resp, err := sendRPC(conn, 1007, RPCProcCompound, cw.Bytes())
	if err != nil {
		t.Fatalf("open-read failed: %v", err)
	}
	cr := NewXDRReader(resp)
	if st, _ := cr.ReadUint32(); st != NFS4OK {
		t.Fatalf("open-read compound error: %d", st)
	}
	_, _ = cr.ReadString()
	_, _ = cr.ReadUint32()
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpLookup)
	mustReadOpStatus(t, cr, OpRead)
	eof, _ := cr.ReadUint32()
	readBytes, _ := cr.ReadBytes()

	if string(readBytes) != "Hello NFSv4 World!" {
		t.Fatalf("unexpected content: %s", string(readBytes))
	}
	if eof != 1 {
		t.Fatalf("expected EOF flag 1, got %d", eof)
	}

	// 2. WRITE new content to "hello.txt"
	newContent := []byte("Overwritten by NFSv4 Write!")
	cw2 := NewXDRWriter()
	cw2.WriteString("write-test")
	cw2.WriteUint32(0)
	cw2.WriteUint32(3)
	cw2.WriteUint32(OpPutrootfh)
	cw2.WriteUint32(OpLookup)
	cw2.WriteString("hello.txt")
	cw2.WriteUint32(OpWrite)
	cw2.WriteFixedBytes(make([]byte, 16)) // stateid
	cw2.WriteUint64(0)                    // offset
	cw2.WriteUint32(2)                    // stable_how = FILE_SYNC4
	cw2.WriteBytes(newContent)

	resp2, err := sendRPC(conn, 1008, RPCProcCompound, cw2.Bytes())
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	cr2 := NewXDRReader(resp2)
	if st, _ := cr2.ReadUint32(); st != NFS4OK {
		t.Fatalf("write compound error: %d", st)
	}
	_, _ = cr2.ReadString()
	_, _ = cr2.ReadUint32()
	mustReadOpStatus(t, cr2, OpPutrootfh)
	mustReadOpStatus(t, cr2, OpLookup)
	mustReadOpStatus(t, cr2, OpWrite)
	writtenCount, _ := cr2.ReadUint32()
	if int(writtenCount) != len(newContent) {
		t.Fatalf("expected %d bytes written, got %d", len(newContent), writtenCount)
	}
}

// Scenario 6: [SPEC-NFS-006] 客户端 IP 白名单访问控制 (IP Whitelist Access Control)
func TestNFS_IP_Whitelist(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "blackhole-nfs-ip-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()

	// Whitelist only allows 192.168.50.240 and 10.0.0.0/24 (127.0.0.1 is deliberately excluded to test reject)
	cfg := &config.Config{
		ShareDir:      tmpDir,
		NFSPort:       port,
		NFSAllowedIPs: "192.168.50.240, 10.0.0.0/24",
	}

	srv := NewServer(cfg)
	go func() {
		_ = srv.Start()
	}()
	defer srv.Shutdown(context.Background())

	// Unit verification of IsIPAllowed
	if !srv.IsIPAllowed("192.168.50.240") {
		t.Fatalf("expected 192.168.50.240 to be allowed")
	}
	if !srv.IsIPAllowed("10.0.0.42") {
		t.Fatalf("expected 10.0.0.42 in subnet to be allowed")
	}
	if srv.IsIPAllowed("127.0.0.1") {
		t.Fatalf("expected 127.0.0.1 to be disallowed")
	}
	if srv.IsIPAllowed("192.168.50.189") {
		t.Fatalf("expected 192.168.50.189 to be disallowed")
	}

	// Wait for server to start
	addr := net.JoinHostPort("127.0.0.1", port)
	time.Sleep(50 * time.Millisecond)

	// Attempt connection from 127.0.0.1 (disallowed)
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err == nil {
		defer conn.Close()
		// Server should immediately close the connection upon unauthorized accept
		buf := make([]byte, 10)
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, readErr := conn.Read(buf)
		if n > 0 || (readErr == nil) {
			t.Fatalf("expected connection to be rejected/closed, got %d bytes", n)
		}
	}
}

// Scenario 7: [SPEC-NFS-007] 目录与软链接创建 (OP_CREATE + OP_READLINK)
func TestNFS_Create_And_Readlink(t *testing.T) {
	_, addr, rootDir, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. OP_CREATE Directory (NF4DIR = 2)
	cw := NewXDRWriter()
	cw.WriteString("create-dir-test")
	cw.WriteUint32(0) // minorver
	cw.WriteUint32(2) // 2 ops: PUTROOTFH, CREATE
	cw.WriteUint32(OpPutrootfh)
	cw.WriteUint32(OpCreate)
	cw.WriteUint32(NF4DIR)             // type = NF4DIR
	cw.WriteString("my_new_dir")       // name
	cw.WriteBitmap([]uint32{0, 0})     // createattrs bitmap
	cw.WriteBytes(nil)                 // opaque vals

	resp, err := sendRPC(conn, 2001, RPCProcCompound, cw.Bytes())
	if err != nil {
		t.Fatalf("create dir failed: %v", err)
	}
	cr := NewXDRReader(resp)
	if st, _ := cr.ReadUint32(); st != NFS4OK {
		t.Fatalf("create dir compound error: %d", st)
	}
	_, _ = cr.ReadString()
	_, _ = cr.ReadUint32()
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpCreate)

	// Verify on disk
	fi, err := os.Stat(filepath.Join(rootDir, "my_new_dir"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected my_new_dir directory on disk: %v", err)
	}

	// 2. OP_CREATE Symlink (NF4LNK = 5) pointing to "hello.txt"
	cw2 := NewXDRWriter()
	cw2.WriteString("create-lnk-test")
	cw2.WriteUint32(0)
	cw2.WriteUint32(2)
	cw2.WriteUint32(OpPutrootfh)
	cw2.WriteUint32(OpCreate)
	cw2.WriteUint32(NF4LNK)
	cw2.WriteString("hello.txt") // link target
	cw2.WriteString("my_symlink") // link name
	cw2.WriteBitmap([]uint32{0, 0})
	cw2.WriteBytes(nil)

	resp2, err := sendRPC(conn, 2002, RPCProcCompound, cw2.Bytes())
	if err != nil {
		t.Fatalf("create symlink failed: %v", err)
	}
	cr2 := NewXDRReader(resp2)
	if st, _ := cr2.ReadUint32(); st != NFS4OK {
		t.Fatalf("create lnk compound error: %d", st)
	}
	_, _ = cr2.ReadString()
	_, _ = cr2.ReadUint32()
	mustReadOpStatus(t, cr2, OpPutrootfh)
	mustReadOpStatus(t, cr2, OpCreate)

	// 3. OP_READLINK on "my_symlink"
	cw3 := NewXDRWriter()
	cw3.WriteString("readlink-test")
	cw3.WriteUint32(0)
	cw3.WriteUint32(3)
	cw3.WriteUint32(OpPutrootfh)
	cw3.WriteUint32(OpLookup)
	cw3.WriteString("my_symlink")
	cw3.WriteUint32(OpReadlink)

	resp3, err := sendRPC(conn, 2003, RPCProcCompound, cw3.Bytes())
	if err != nil {
		t.Fatalf("readlink failed: %v", err)
	}
	cr3 := NewXDRReader(resp3)
	if st, _ := cr3.ReadUint32(); st != NFS4OK {
		t.Fatalf("readlink compound error: %d", st)
	}
	_, _ = cr3.ReadString()
	_, _ = cr3.ReadUint32()
	mustReadOpStatus(t, cr3, OpPutrootfh)
	mustReadOpStatus(t, cr3, OpLookup)
	mustReadOpStatus(t, cr3, OpReadlink)
	linkTarget, _ := cr3.ReadString()
	if linkTarget != "hello.txt" {
		t.Fatalf("expected link target hello.txt, got %s", linkTarget)
	}
}

// Scenario 8: [SPEC-NFS-008] 文件与目录重命名与跨目录移动 (OP_RENAME)
func TestNFS_Rename(t *testing.T) {
	_, addr, rootDir, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// Put a file inside public: public/move_me.txt
	subFile := filepath.Join(rootDir, "public", "move_me.txt")
	if err := os.WriteFile(subFile, []byte("moving"), 0644); err != nil {
		t.Fatalf("failed to create subFile: %v", err)
	}

	// Rename public/move_me.txt -> moved_root.txt
	// SFH: public (SAVED_FH)
	// CFH: root (CURRENT_FH)
	cw := NewXDRWriter()
	cw.WriteString("rename-test")
	cw.WriteUint32(0)
	cw.WriteUint32(5)
	cw.WriteUint32(OpPutrootfh)
	cw.WriteUint32(OpLookup)
	cw.WriteString("public")
	cw.WriteUint32(OpSavefh)    // SFH = public
	cw.WriteUint32(OpPutrootfh) // CFH = root
	cw.WriteUint32(OpRename)
	cw.WriteString("move_me.txt")
	cw.WriteString("moved_root.txt")

	resp, err := sendRPC(conn, 2004, RPCProcCompound, cw.Bytes())
	if err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	cr := NewXDRReader(resp)
	if st, _ := cr.ReadUint32(); st != NFS4OK {
		t.Fatalf("rename compound error: %d", st)
	}
	_, _ = cr.ReadString()
	_, _ = cr.ReadUint32()
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpLookup)
	mustReadOpStatus(t, cr, OpSavefh)
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpRename)

	// Check on disk
	if _, err := os.Stat(filepath.Join(rootDir, "moved_root.txt")); err != nil {
		t.Fatalf("expected moved_root.txt in root dir: %v", err)
	}
	if _, err := os.Stat(subFile); !os.IsNotExist(err) {
		t.Fatalf("expected old subFile to be gone: %v", err)
	}
}

// Scenario 9: [SPEC-NFS-009] 显式数据持久化刷盘确认 (OP_COMMIT)
func TestNFS_Commit(t *testing.T) {
	_, addr, _, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	cw := NewXDRWriter()
	cw.WriteString("commit-test")
	cw.WriteUint32(0)
	cw.WriteUint32(3)
	cw.WriteUint32(OpPutrootfh)
	cw.WriteUint32(OpLookup)
	cw.WriteString("hello.txt")
	cw.WriteUint32(OpCommit)
	cw.WriteUint64(0)    // offset
	cw.WriteUint32(4096) // count

	resp, err := sendRPC(conn, 2005, RPCProcCompound, cw.Bytes())
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	cr := NewXDRReader(resp)
	if st, _ := cr.ReadUint32(); st != NFS4OK {
		t.Fatalf("commit compound error: %d", st)
	}
	_, _ = cr.ReadString()
	_, _ = cr.ReadUint32()
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpLookup)
	mustReadOpStatus(t, cr, OpCommit)
}

// Scenario 10: [SPEC-NFS-010] 硬链接创建与文件引用关联 (OP_LINK)
func TestNFS_Hardlink(t *testing.T) {
	_, addr, rootDir, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// SFH: hello.txt, CFH: root
	cw := NewXDRWriter()
	cw.WriteString("link-test")
	cw.WriteUint32(0)
	cw.WriteUint32(5)
	cw.WriteUint32(OpPutrootfh)
	cw.WriteUint32(OpLookup)
	cw.WriteString("hello.txt")
	cw.WriteUint32(OpSavefh)    // SFH = hello.txt
	cw.WriteUint32(OpPutrootfh) // CFH = root
	cw.WriteUint32(OpLink)
	cw.WriteString("hello_hl.txt")

	resp, err := sendRPC(conn, 3001, RPCProcCompound, cw.Bytes())
	if err != nil {
		t.Fatalf("hardlink failed: %v", err)
	}
	cr := NewXDRReader(resp)
	if st, _ := cr.ReadUint32(); st != NFS4OK {
		t.Fatalf("hardlink compound error: %d", st)
	}
	_, _ = cr.ReadString()
	_, _ = cr.ReadUint32()
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpLookup)
	mustReadOpStatus(t, cr, OpSavefh)
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpLink)

	// Inode comparison
	fiOrig, err1 := os.Stat(filepath.Join(rootDir, "hello.txt"))
	fiLink, err2 := os.Stat(filepath.Join(rootDir, "hello_hl.txt"))
	if err1 != nil || err2 != nil {
		t.Fatalf("failed to stat link or orig: %v, %v", err1, err2)
	}
	stat1, ok1 := fiOrig.Sys().(*syscall.Stat_t)
	stat2, ok2 := fiLink.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 || stat1.Ino != stat2.Ino {
		t.Fatalf("expected identical inode for hardlink: %d vs %d", stat1.Ino, stat2.Ino)
	}
}

// Scenario 11: [SPEC-NFS-011] 文件锁授予与冲突拒绝 (OP_LOCK + OP_LOCKT)
func TestNFS_Lock_Conflict(t *testing.T) {
	_, addr, _, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. Client A acquires exclusive write lock (LockTypeWrite = 2) on 0..1024
	cw := NewXDRWriter()
	cw.WriteString("lock-acquire-test")
	cw.WriteUint32(0)
	cw.WriteUint32(3)
	cw.WriteUint32(OpPutrootfh)
	cw.WriteUint32(OpLookup)
	cw.WriteString("hello.txt")
	cw.WriteUint32(OpLock)
	cw.WriteUint32(LockTypeWrite) // locktype = WRITE
	cw.WriteUint32(0)             // reclaim = false
	cw.WriteUint64(0)             // offset = 0
	cw.WriteUint64(1024)          // length = 1024
	cw.WriteUint32(1)             // new_lock_owner = true
	cw.WriteUint32(1)             // open_seqid
	cw.WriteFixedBytes(make([]byte, 16))
	cw.WriteUint32(1)             // lock_seqid
	cw.WriteUint64(100)           // clientID = 100
	cw.WriteString("clientA")     // owner = clientA

	resp, err := sendRPC(conn, 3002, RPCProcCompound, cw.Bytes())
	if err != nil {
		t.Fatalf("lock acquire failed: %v", err)
	}
	cr := NewXDRReader(resp)
	if st, _ := cr.ReadUint32(); st != NFS4OK {
		t.Fatalf("lock compound error: %d", st)
	}
	_, _ = cr.ReadString()
	_, _ = cr.ReadUint32()
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpLookup)
	mustReadOpStatus(t, cr, OpLock)
	lockStateID, _ := cr.ReadFixedBytes(16)
	if len(lockStateID) != 16 {
		t.Fatalf("expected 16-byte lockStateID")
	}

	// 2. Client B tests overlapping lock using OP_LOCKT
	cw2 := NewXDRWriter()
	cw2.WriteString("lock-test-conflict")
	cw2.WriteUint32(0)
	cw2.WriteUint32(3)
	cw2.WriteUint32(OpPutrootfh)
	cw2.WriteUint32(OpLookup)
	cw2.WriteString("hello.txt")
	cw2.WriteUint32(OpLockt)
	cw2.WriteUint32(LockTypeWrite)
	cw2.WriteUint64(512)
	cw2.WriteUint64(512)
	cw2.WriteUint64(200) // clientID = 200
	cw2.WriteString("clientB")

	resp2, err := sendRPC(conn, 3003, RPCProcCompound, cw2.Bytes())
	if err != nil {
		t.Fatalf("lockt request failed: %v", err)
	}
	cr2 := NewXDRReader(resp2)
	status, _ := cr2.ReadUint32()
	if status != NFS4ERRDENIED {
		t.Fatalf("expected compound status NFS4ERRDENIED (%d), got %d", NFS4ERRDENIED, status)
	}
}

// Scenario 12: [SPEC-NFS-012] 文件解锁与锁持有者注销 (OP_LOCKU + OP_RELEASE_LOCKOWNER)
func TestNFS_Lock_Unlock_Release(t *testing.T) {
	_, addr, _, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. Acquire lock
	cw := NewXDRWriter()
	cw.WriteString("lock-then-unlock")
	cw.WriteUint32(0)
	cw.WriteUint32(3)
	cw.WriteUint32(OpPutrootfh)
	cw.WriteUint32(OpLookup)
	cw.WriteString("hello.txt")
	cw.WriteUint32(OpLock)
	cw.WriteUint32(LockTypeWrite)
	cw.WriteUint32(0)
	cw.WriteUint64(0)
	cw.WriteUint64(1024)
	cw.WriteUint32(1)
	cw.WriteUint32(1)
	cw.WriteFixedBytes(make([]byte, 16))
	cw.WriteUint32(1)
	cw.WriteUint64(555)
	cw.WriteString("owner555")

	resp, _ := sendRPC(conn, 3004, RPCProcCompound, cw.Bytes())
	cr := NewXDRReader(resp)
	_, _ = cr.ReadUint32()
	_, _ = cr.ReadString()
	_, _ = cr.ReadUint32()
	mustReadOpStatus(t, cr, OpPutrootfh)
	mustReadOpStatus(t, cr, OpLookup)
	mustReadOpStatus(t, cr, OpLock)
	sid, _ := cr.ReadFixedBytes(16)

	// 2. Unlock using OP_LOCKU
	cw2 := NewXDRWriter()
	cw2.WriteString("unlock-test")
	cw2.WriteUint32(0)
	cw2.WriteUint32(3)
	cw2.WriteUint32(OpPutrootfh)
	cw2.WriteUint32(OpLookup)
	cw2.WriteString("hello.txt")
	cw2.WriteUint32(OpLocku)
	cw2.WriteUint32(LockTypeWrite)
	cw2.WriteUint32(2) // seqid
	cw2.WriteFixedBytes(sid)
	cw2.WriteUint64(0)
	cw2.WriteUint64(1024)

	resp2, err := sendRPC(conn, 3005, RPCProcCompound, cw2.Bytes())
	if err != nil {
		t.Fatalf("locku failed: %v", err)
	}
	cr2 := NewXDRReader(resp2)
	if st, _ := cr2.ReadUint32(); st != NFS4OK {
		t.Fatalf("locku compound error: %d", st)
	}

	// 3. OP_RELEASE_LOCKOWNER
	cw3 := NewXDRWriter()
	cw3.WriteString("release-owner")
	cw3.WriteUint32(0)
	cw3.WriteUint32(1)
	cw3.WriteUint32(OpReleaseLockowner)
	cw3.WriteUint64(555)
	cw3.WriteString("owner555")

	resp3, err := sendRPC(conn, 3006, RPCProcCompound, cw3.Bytes())
	if err != nil {
		t.Fatalf("release lockowner failed: %v", err)
	}
	cr3 := NewXDRReader(resp3)
	if st, _ := cr3.ReadUint32(); st != NFS4OK {
		t.Fatalf("release lockowner compound error: %d", st)
	}
}

func mustReadOpStatus(t *testing.T, r *XDRReader, expectedOp uint32) uint32 {
	t.Helper()
	op, err := r.ReadUint32()
	if err != nil {
		t.Fatalf("failed to read op num: %v", err)
	}
	if op != expectedOp {
		t.Fatalf("expected op %d, got %d", expectedOp, op)
	}
	st, err := r.ReadUint32()
	if err != nil {
		t.Fatalf("failed to read op status: %v", err)
	}
	if st != NFS4OK {
		t.Fatalf("expected op %d status NFS4OK, got %d", expectedOp, st)
	}
	return st
}

func readCompoundHeader(t *testing.T, cr *XDRReader) (status uint32, tag string, resCount uint32) {
	t.Helper()
	var err error
	status, err = cr.ReadUint32()
	if err != nil {
		t.Fatalf("failed to read compound status: %v", err)
	}
	tag, err = cr.ReadString()
	if err != nil {
		t.Fatalf("failed to read compound tag: %v", err)
	}
	resCount, err = cr.ReadUint32()
	if err != nil {
		t.Fatalf("failed to read compound resCount: %v", err)
	}
	return status, tag, resCount
}

func skipSequenceRes(cr *XDRReader) {
	_, _ = cr.ReadFixedBytes(16) // sessID
	_, _ = cr.ReadUint32()       // seqID
	_, _ = cr.ReadUint32()       // slotID
	_, _ = cr.ReadUint32()       // highest_slotid
	_, _ = cr.ReadUint32()       // target_highest_slotid
	_, _ = cr.ReadUint32()       // status_flags
}

// Scenario 13: [SPEC-NFS-013] NFSv4.1/4.2 会话握手与槽位序列管理 (EXCHANGE_ID + CREATE_SESSION + SEQUENCE)
func TestNFS_V42_Session_Handshake(t *testing.T) {
	_, addr, _, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. OP_EXCHANGE_ID (minorversion = 2)
	cw1 := NewXDRWriter()
	cw1.WriteString("test-exchgid")
	cw1.WriteUint32(2) // minorversion = 2 (NFSv4.2)
	cw1.WriteUint32(1) // arg count = 1
	cw1.WriteUint32(OpExchangeId)
	cw1.WriteFixedBytes([]byte("verifier")) // co_verifier (8B)
	cw1.WriteString("client-owner-v42")     // co_ownerid
	cw1.WriteUint32(0)                      // eia_flags
	cw1.WriteUint32(0)                      // spa_how = SP4_NONE
	cw1.WriteUint32(0)                      // client_impl_id count = 0

	resp1, err := sendRPC(conn, 4001, RPCProcCompound, cw1.Bytes())
	if err != nil {
		t.Fatalf("exchange_id RPC failed: %v", err)
	}
	cr1 := NewXDRReader(resp1)
	if st, _, _ := readCompoundHeader(t, cr1); st != NFS4OK {
		t.Fatalf("exchange_id compound error: %d", st)
	}
	mustReadOpStatus(t, cr1, OpExchangeId)
	clientID, _ := cr1.ReadUint64()
	if clientID == 0 {
		t.Fatalf("expected non-zero clientID")
	}
	seqID, _ := cr1.ReadUint32()
	if seqID != 1 {
		t.Fatalf("expected sequenceID 1, got %d", seqID)
	}
	flags, _ := cr1.ReadUint32()
	if flags&Exchgid4FlagUseNonPnfs == 0 {
		t.Fatalf("expected Exchgid4FlagUseNonPnfs in flags, got 0x%x", flags)
	}
	// skip state_protect4_r, server_owner, scope, impl_id
	_, _ = cr1.ReadUint32() // spr_how
	_, _ = cr1.ReadUint64() // minor_id
	_, _ = cr1.ReadBytes()  // major_id
	_, _ = cr1.ReadBytes()  // server_scope
	_, _ = cr1.ReadUint32() // impl_id count

	// 2. OP_CREATE_SESSION
	cw2 := NewXDRWriter()
	cw2.WriteString("test-createsess")
	cw2.WriteUint32(2)
	cw2.WriteUint32(1)
	cw2.WriteUint32(OpCreateSession)
	cw2.WriteUint64(clientID)
	cw2.WriteUint32(1) // csa_sequence
	cw2.WriteUint32(0) // csa_flags
	// fore channel attrs
	cw2.WriteUint32(0)       // headerpadsize
	cw2.WriteUint32(1048576) // maxreq
	cw2.WriteUint32(1048576) // maxresp
	cw2.WriteUint32(4096)    // maxresp_cached
	cw2.WriteUint32(64)      // maxops
	cw2.WriteUint32(64)      // maxrequests
	cw2.WriteUint32(0)       // rdma_ird count
	// back channel attrs
	cw2.WriteUint32(0)    // headerpadsize
	cw2.WriteUint32(4096) // maxreq
	cw2.WriteUint32(4096) // maxresp
	cw2.WriteUint32(0)    // maxresp_cached
	cw2.WriteUint32(16)   // maxops
	cw2.WriteUint32(16)   // maxrequests
	cw2.WriteUint32(0)    // rdma_ird count
	cw2.WriteUint32(0)    // cb_program
	cw2.WriteUint32(0)    // sec_parms count

	resp2, err := sendRPC(conn, 4002, RPCProcCompound, cw2.Bytes())
	if err != nil {
		t.Fatalf("create_session RPC failed: %v", err)
	}
	cr2 := NewXDRReader(resp2)
	if st, _, _ := readCompoundHeader(t, cr2); st != NFS4OK {
		t.Fatalf("create_session compound error: %d", st)
	}
	mustReadOpStatus(t, cr2, OpCreateSession)
	sessBytes, _ := cr2.ReadFixedBytes(16)
	var sessionID [16]byte
	copy(sessionID[:], sessBytes)

	// 3. OP_SEQUENCE (seq=1, slot=0)
	cw3 := NewXDRWriter()
	cw3.WriteString("test-seq")
	cw3.WriteUint32(2)
	cw3.WriteUint32(1)
	cw3.WriteUint32(OpSequence)
	cw3.WriteFixedBytes(sessionID[:])
	cw3.WriteUint32(1) // seqID
	cw3.WriteUint32(0) // slotID
	cw3.WriteUint32(0) // highestSlotID
	cw3.WriteUint32(0) // cachethis

	resp3, err := sendRPC(conn, 4003, RPCProcCompound, cw3.Bytes())
	if err != nil {
		t.Fatalf("sequence RPC failed: %v", err)
	}
	cr3 := NewXDRReader(resp3)
	if st, _, _ := readCompoundHeader(t, cr3); st != NFS4OK {
		t.Fatalf("sequence compound error: %d", st)
	}
	mustReadOpStatus(t, cr3, OpSequence)

	// 4. Test Replay (seq=1, slot=0) -> must succeed
	resp4, err := sendRPC(conn, 4004, RPCProcCompound, cw3.Bytes())
	if err != nil {
		t.Fatalf("sequence replay RPC failed: %v", err)
	}
	cr4 := NewXDRReader(resp4)
	if st, _, _ := readCompoundHeader(t, cr4); st != NFS4OK {
		t.Fatalf("expected replay sequence to succeed, got %d", st)
	}

	// 5. Test Misordered Sequence (seq=99, slot=0) -> must fail with NFS4ERRSEQMISORDERED
	cw5 := NewXDRWriter()
	cw5.WriteString("test-seq-misorder")
	cw5.WriteUint32(2)
	cw5.WriteUint32(1)
	cw5.WriteUint32(OpSequence)
	cw5.WriteFixedBytes(sessionID[:])
	cw5.WriteUint32(99) // jumped seqID
	cw5.WriteUint32(0)  // slotID
	cw5.WriteUint32(0)
	cw5.WriteUint32(0)

	resp5, err := sendRPC(conn, 4005, RPCProcCompound, cw5.Bytes())
	if err != nil {
		t.Fatalf("sequence misordered RPC failed: %v", err)
	}
	cr5 := NewXDRReader(resp5)
	if st, _, _ := readCompoundHeader(t, cr5); st != NFS4ERRSEQMISORDERED {
		t.Fatalf("expected NFS4ERRSEQMISORDERED, got %d", st)
	}

	// 6. OP_RECLAIM_COMPLETE
	cw6 := NewXDRWriter()
	cw6.WriteString("test-reclaim")
	cw6.WriteUint32(2)
	cw6.WriteUint32(2)
	cw6.WriteUint32(OpSequence)
	cw6.WriteFixedBytes(sessionID[:])
	cw6.WriteUint32(2) // increment to seq=2
	cw6.WriteUint32(0) // slot 0
	cw6.WriteUint32(0)
	cw6.WriteUint32(0)
	cw6.WriteUint32(OpReclaimComplete)
	cw6.WriteUint32(0) // rca_one_fs = false

	resp6, err := sendRPC(conn, 4006, RPCProcCompound, cw6.Bytes())
	if err != nil {
		t.Fatalf("reclaim_complete RPC failed: %v", err)
	}
	cr6 := NewXDRReader(resp6)
	if st, _, _ := readCompoundHeader(t, cr6); st != NFS4OK {
		t.Fatalf("reclaim_complete compound error: %d", st)
	}

	// 7. OP_DESTROY_SESSION
	cw7 := NewXDRWriter()
	cw7.WriteString("test-destroy-sess")
	cw7.WriteUint32(2)
	cw7.WriteUint32(1)
	cw7.WriteUint32(OpDestroySession)
	cw7.WriteFixedBytes(sessionID[:])

	resp7, err := sendRPC(conn, 4007, RPCProcCompound, cw7.Bytes())
	if err != nil {
		t.Fatalf("destroy session RPC failed: %v", err)
	}
	cr7 := NewXDRReader(resp7)
	if st, _, _ := readCompoundHeader(t, cr7); st != NFS4OK {
		t.Fatalf("destroy session compound error: %d", st)
	}

	// 8. Verify Sequence on destroyed session returns NFS4ERRBADSESSION
	resp8, err := sendRPC(conn, 4008, RPCProcCompound, cw3.Bytes())
	if err != nil {
		t.Fatalf("sequence RPC after destroy failed: %v", err)
	}
	cr8 := NewXDRReader(resp8)
	if st, _, _ := readCompoundHeader(t, cr8); st != NFS4ERRBADSESSION {
		t.Fatalf("expected NFS4ERRBADSESSION on destroyed session, got %d", st)
	}
}

// Scenario 14: [SPEC-NFS-014] NFSv4.2 服务端直拷零网络流量传输 (OP_COPY)
func TestNFS_V42_ServerSideCopy(t *testing.T) {
	_, addr, tmpDir, cleanup := setupTestNFSServer(t)
	defer cleanup()

	// Prepare source file with 64KB data
	srcData := make([]byte, 64*1024)
	for i := range srcData {
		srcData[i] = byte(i % 256)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "huge_src.dat"), srcData, 0644); err != nil {
		t.Fatalf("failed to write huge_src.dat: %v", err)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. Session Handshake
	cw1 := NewXDRWriter()
	cw1.WriteString("copy-exchgid")
	cw1.WriteUint32(2)
	cw1.WriteUint32(1)
	cw1.WriteUint32(OpExchangeId)
	cw1.WriteFixedBytes([]byte("copy-ver"))
	cw1.WriteString("copy-client")
	cw1.WriteUint32(0)
	cw1.WriteUint32(0)
	cw1.WriteUint32(0)

	resp1, _ := sendRPC(conn, 5001, RPCProcCompound, cw1.Bytes())
	cr1 := NewXDRReader(resp1)
	readCompoundHeader(t, cr1)
	mustReadOpStatus(t, cr1, OpExchangeId)
	clientID, _ := cr1.ReadUint64()

	cw2 := NewXDRWriter()
	cw2.WriteString("copy-sess")
	cw2.WriteUint32(2)
	cw2.WriteUint32(1)
	cw2.WriteUint32(OpCreateSession)
	cw2.WriteUint64(clientID)
	cw2.WriteUint32(1)
	cw2.WriteUint32(0)
	cw2.WriteUint32(0)
	cw2.WriteUint32(1048576)
	cw2.WriteUint32(1048576)
	cw2.WriteUint32(4096)
	cw2.WriteUint32(64)
	cw2.WriteUint32(64)
	cw2.WriteUint32(0)
	cw2.WriteUint32(0)
	cw2.WriteUint32(4096)
	cw2.WriteUint32(4096)
	cw2.WriteUint32(0)
	cw2.WriteUint32(16)
	cw2.WriteUint32(16)
	cw2.WriteUint32(0)
	cw2.WriteUint32(0)
	cw2.WriteUint32(0)

	resp2, _ := sendRPC(conn, 5002, RPCProcCompound, cw2.Bytes())
	cr2 := NewXDRReader(resp2)
	readCompoundHeader(t, cr2)
	mustReadOpStatus(t, cr2, OpCreateSession)
	sessBytes, _ := cr2.ReadFixedBytes(16)

	// 2. Perform OP_COPY from huge_src.dat to huge_dst.dat
	cw3 := NewXDRWriter()
	cw3.WriteString("server-side-copy")
	cw3.WriteUint32(2)
	cw3.WriteUint32(7)
	// Op 1: Sequence
	cw3.WriteUint32(OpSequence)
	cw3.WriteFixedBytes(sessBytes)
	cw3.WriteUint32(1)
	cw3.WriteUint32(0)
	cw3.WriteUint32(0)
	cw3.WriteUint32(0)
	// Op 2: PutRootFH
	cw3.WriteUint32(OpPutrootfh)
	// Op 3: Lookup huge_src.dat
	cw3.WriteUint32(OpLookup)
	cw3.WriteString("huge_src.dat")
	// Op 4: SaveFH (source)
	cw3.WriteUint32(OpSavefh)
	// Op 5: PutRootFH
	cw3.WriteUint32(OpPutrootfh)
	// Op 6: Open huge_dst.dat (create if not exists)
	cw3.WriteUint32(OpOpen)
	cw3.WriteUint32(1) // seqid
	cw3.WriteUint32(2) // share_access = OPEN4_SHARE_ACCESS_WRITE
	cw3.WriteUint32(0) // share_deny = 0
	cw3.WriteUint64(clientID)
	cw3.WriteString("copy-owner")
	cw3.WriteUint32(1) // openhow = OPEN4_CREATE
	cw3.WriteUint32(0) // UNCHECKED4
	cw3.WriteBitmap([]uint32{0, 0})
	cw3.WriteBytes(nil)
	cw3.WriteUint32(0) // CLAIM_NULL
	cw3.WriteString("huge_dst.dat")
	// Op 7: OP_COPY
	cw3.WriteUint32(OpCopy)
	cw3.WriteFixedBytes(make([]byte, 16)) // src_stateid
	cw3.WriteFixedBytes(make([]byte, 16)) // dst_stateid
	cw3.WriteUint64(0)                    // ca_src_offset
	cw3.WriteUint64(0)                    // ca_dst_offset
	cw3.WriteUint64(uint64(len(srcData))) // ca_count = 64KB
	cw3.WriteUint32(1)                    // ca_consecutive = true
	cw3.WriteUint32(1)                    // ca_synchronous = true
	cw3.WriteUint32(0)                    // netloc count = 0

	resp3, err := sendRPC(conn, 5003, RPCProcCompound, cw3.Bytes())
	if err != nil {
		t.Fatalf("server-side copy RPC failed: %v", err)
	}
	cr3 := NewXDRReader(resp3)
	if st, _, _ := readCompoundHeader(t, cr3); st != NFS4OK {
		t.Fatalf("server-side copy compound error: %d", st)
	}
	mustReadOpStatus(t, cr3, OpSequence)
	skipSequenceRes(cr3)
	mustReadOpStatus(t, cr3, OpPutrootfh)
	mustReadOpStatus(t, cr3, OpLookup)
	mustReadOpStatus(t, cr3, OpSavefh)
	mustReadOpStatus(t, cr3, OpPutrootfh)
	mustReadOpStatus(t, cr3, OpOpen)
	// skip Open results
	_, _ = cr3.ReadFixedBytes(16) // stateid
	_, _ = cr3.ReadUint32()       // atomic
	_, _ = cr3.ReadUint64()       // cinfo before
	_, _ = cr3.ReadUint64()       // cinfo after
	_, _ = cr3.ReadUint32()       // rflags
	_, _ = cr3.ReadBitmap()       // attrset
	_, _ = cr3.ReadUint32()       // delegation

	mustReadOpStatus(t, cr3, OpCopy)
	cbCount, _ := cr3.ReadUint32()
	if cbCount != 0 {
		t.Fatalf("expected cbCount 0 for sync copy, got %d", cbCount)
	}
	copiedCount, _ := cr3.ReadUint64()
	if copiedCount != uint64(len(srcData)) {
		t.Fatalf("expected copied count %d, got %d", len(srcData), copiedCount)
	}
	committed, _ := cr3.ReadUint32()
	if committed != 2 { // FILE_SYNC4
		t.Fatalf("expected FILE_SYNC4 (2), got %d", committed)
	}
	_, _ = cr3.ReadFixedBytes(8) // verifier
	consec, _ := cr3.ReadUint32()
	syncMode, _ := cr3.ReadUint32()
	if consec != 1 || syncMode != 1 {
		t.Fatalf("expected consecutive and synchronous true")
	}

	// 3. Verify destination file on local filesystem
	dstBytes, err := os.ReadFile(filepath.Join(tmpDir, "huge_dst.dat"))
	if err != nil {
		t.Fatalf("failed to read copied destination file: %v", err)
	}
	if len(dstBytes) != len(srcData) {
		t.Fatalf("destination file length mismatch: %d vs %d", len(dstBytes), len(srcData))
	}
	for i := range srcData {
		if dstBytes[i] != srcData[i] {
			t.Fatalf("byte mismatch at offset %d: got %x, expected %x", i, dstBytes[i], srcData[i])
		}
	}
}

// Scenario 15: [SPEC-NFS-015] NFSv4.2 空间快速预分配与稀疏文件打洞 (OP_ALLOCATE + OP_DEALLOCATE + OP_SEEK)
func TestNFS_V42_Allocate_Deallocate(t *testing.T) {
	_, addr, tmpDir, cleanup := setupTestNFSServer(t)
	defer cleanup()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. Session Handshake
	cw1 := NewXDRWriter()
	cw1.WriteString("alloc-exchgid")
	cw1.WriteUint32(2)
	cw1.WriteUint32(1)
	cw1.WriteUint32(OpExchangeId)
	cw1.WriteFixedBytes([]byte("allocver"))
	cw1.WriteString("alloc-client")
	cw1.WriteUint32(0)
	cw1.WriteUint32(0)
	cw1.WriteUint32(0)

	resp1, _ := sendRPC(conn, 6001, RPCProcCompound, cw1.Bytes())
	cr1 := NewXDRReader(resp1)
	readCompoundHeader(t, cr1)
	mustReadOpStatus(t, cr1, OpExchangeId)
	clientID, _ := cr1.ReadUint64()

	cw2 := NewXDRWriter()
	cw2.WriteString("alloc-sess")
	cw2.WriteUint32(2)
	cw2.WriteUint32(1)
	cw2.WriteUint32(OpCreateSession)
	cw2.WriteUint64(clientID)
	cw2.WriteUint32(1)
	cw2.WriteUint32(0)
	cw2.WriteUint32(0)
	cw2.WriteUint32(1048576)
	cw2.WriteUint32(1048576)
	cw2.WriteUint32(4096)
	cw2.WriteUint32(64)
	cw2.WriteUint32(64)
	cw2.WriteUint32(0)
	cw2.WriteUint32(0)
	cw2.WriteUint32(4096)
	cw2.WriteUint32(4096)
	cw2.WriteUint32(0)
	cw2.WriteUint32(16)
	cw2.WriteUint32(16)
	cw2.WriteUint32(0)
	cw2.WriteUint32(0)
	cw2.WriteUint32(0)

	resp2, _ := sendRPC(conn, 6002, RPCProcCompound, cw2.Bytes())
	cr2 := NewXDRReader(resp2)
	readCompoundHeader(t, cr2)
	mustReadOpStatus(t, cr2, OpCreateSession)
	sessBytes, _ := cr2.ReadFixedBytes(16)

	// 2. OP_ALLOCATE 1MB on hello.txt
	cwClean := NewXDRWriter()
	cwClean.WriteString("allocate-test")
	cwClean.WriteUint32(2)
	cwClean.WriteUint32(4)
	cwClean.WriteUint32(OpSequence)
	cwClean.WriteFixedBytes(sessBytes)
	cwClean.WriteUint32(1)
	cwClean.WriteUint32(0)
	cwClean.WriteUint32(0)
	cwClean.WriteUint32(0)
	cwClean.WriteUint32(OpPutrootfh)
	cwClean.WriteUint32(OpLookup)
	cwClean.WriteString("hello.txt")
	cwClean.WriteUint32(OpAllocate)
	cwClean.WriteFixedBytes(make([]byte, 16))
	cwClean.WriteUint64(0)
	cwClean.WriteUint64(1048576)

	resp3, err := sendRPC(conn, 6003, RPCProcCompound, cwClean.Bytes())
	if err != nil {
		t.Fatalf("allocate RPC failed: %v", err)
	}
	cr3 := NewXDRReader(resp3)
	if st, _, _ := readCompoundHeader(t, cr3); st != NFS4OK {
		t.Fatalf("allocate compound error: %d", st)
	}
	mustReadOpStatus(t, cr3, OpSequence)
	skipSequenceRes(cr3)
	mustReadOpStatus(t, cr3, OpPutrootfh)
	mustReadOpStatus(t, cr3, OpLookup)
	mustReadOpStatus(t, cr3, OpAllocate)

	// Verify file size on disk is now 1MB
	fi, err := os.Stat(filepath.Join(tmpDir, "hello.txt"))
	if err != nil {
		t.Fatalf("stat hello.txt failed: %v", err)
	}
	if fi.Size() < 1048576 {
		t.Fatalf("expected file size >= 1048576, got %d", fi.Size())
	}

	// 3. OP_DEALLOCATE and OP_SEEK
	cw4 := NewXDRWriter()
	cw4.WriteString("dealloc-seek-test")
	cw4.WriteUint32(2)
	cw4.WriteUint32(5)
	cw4.WriteUint32(OpSequence)
	cw4.WriteFixedBytes(sessBytes)
	cw4.WriteUint32(2)
	cw4.WriteUint32(0)
	cw4.WriteUint32(0)
	cw4.WriteUint32(0)
	cw4.WriteUint32(OpPutrootfh)
	cw4.WriteUint32(OpLookup)
	cw4.WriteString("hello.txt")
	cw4.WriteUint32(OpDeallocate)
	cw4.WriteFixedBytes(make([]byte, 16))
	cw4.WriteUint64(0)
	cw4.WriteUint64(524288)
	cw4.WriteUint32(OpSeek)
	cw4.WriteFixedBytes(make([]byte, 16))
	cw4.WriteUint64(0) // offset
	cw4.WriteUint32(0) // what = DATA

	resp4, err := sendRPC(conn, 6004, RPCProcCompound, cw4.Bytes())
	if err != nil {
		t.Fatalf("deallocate/seek RPC failed: %v", err)
	}
	cr4 := NewXDRReader(resp4)
	if st, _, _ := readCompoundHeader(t, cr4); st != NFS4OK {
		t.Fatalf("dealloc/seek compound error: %d", st)
	}
	mustReadOpStatus(t, cr4, OpSequence)
	skipSequenceRes(cr4)
	mustReadOpStatus(t, cr4, OpPutrootfh)
	mustReadOpStatus(t, cr4, OpLookup)
	mustReadOpStatus(t, cr4, OpDeallocate)
	mustReadOpStatus(t, cr4, OpSeek)
	eof, _ := cr4.ReadUint32()
	seekOff, _ := cr4.ReadUint64()
	if eof != 0 || seekOff != 0 {
		t.Fatalf("expected seek DATA at offset 0, got eof=%d, off=%d", eof, seekOff)
	}
}
