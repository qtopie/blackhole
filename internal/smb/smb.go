package smb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"unicode/utf16"

	"github.com/qtopie/blackhole/internal/config"
)

// SMB2 Command Codes
const (
	CommandNegotiate    uint16 = 0x0000
	CommandSessionSetup uint16 = 0x0001
	CommandTreeConnect  uint16 = 0x0003
	CommandTreeDisconn  uint16 = 0x0004
	CommandCreate       uint16 = 0x0005
	CommandClose        uint16 = 0x0006
	CommandRead         uint16 = 0x0008
	CommandWrite        uint16 = 0x0009
	CommandIoctl        uint16 = 0x000B
	CommandQueryInfo    uint16 = 0x0010
)

// SMB2 Header Structure (64 bytes)
type Header struct {
	ProtocolID    [4]byte // 0xFE, 'S', 'M', 'B'
	StructureSize uint16  // 64
	CreditCharge  uint16
	Status        uint32
	Command       uint16
	CreditReqResp uint16
	Flags         uint32
	NextCommand   uint32
	MessageID     uint64
	ProcessID     uint32
	TreeID        uint32
	SessionID     uint64
	Signature     [16]byte
}

type Server struct {
	cfg        *config.Config
	listener   net.Listener
	openFiles  sync.Map
	queryIndex int
}

func NewServer(cfg *config.Config) *Server {
	return &Server{
		cfg: cfg,
	}
}

func (s *Server) Start() error {
	addr := ":" + s.cfg.SMBPort
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind SMB server on port %s: %w", s.cfg.SMBPort, err)
	}
	s.listener = l
	fmt.Printf("🚀 Native SMB2/SMB3 protocol server listening on port :%s (Share: %s)\n", s.cfg.SMBPort, s.cfg.ShareDir)

	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-context.Background().Done():
				return nil
			default:
				return nil
			}
		}
		go s.handleConnection(conn)
	}
}

type handleState struct {
	hasQueried bool
	entries    []os.DirEntry
	cursor     int
}

type connState struct {
	nextHandleID uint64
	handles      map[uint64]*handleState
	ipcTrees     map[uint32]bool // TreeIDs connected to IPC$
}

func newConnState() *connState {
	return &connState{
		handles:  make(map[uint64]*handleState),
		ipcTrees: make(map[uint32]bool),
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	state := newConnState()

	for {
		// Read NetBIOS session header (4 bytes: 0x00 + 3-byte length)
		netbiosHeader := make([]byte, 4)
		if _, err := io.ReadFull(conn, netbiosHeader); err != nil {
			return
		}

		length := int(netbiosHeader[1])<<16 | int(netbiosHeader[2])<<8 | int(netbiosHeader[3])
		if length == 0 || length > 1024*1024 {
			return
		}

		packet := make([]byte, length)
		if _, err := io.ReadFull(conn, packet); err != nil {
			return
		}

		if len(packet) < 64 {
			return
		}

		// Check Protocol Magic: 0xFE 'S' 'M' 'B'
		if packet[0] != 0xFE || packet[1] != 'S' || packet[2] != 'M' || packet[3] != 'B' {
			return
		}

		var hdr Header
		buf := bytes.NewReader(packet[:64])
		if err := binary.Read(buf, binary.LittleEndian, &hdr); err != nil {
			return
		}

		reqBody := packet[64:]
		fmt.Printf("DEBUG SMB Command: 0x%04X, len: %d, Status: 0x%08X, SessionID: 0x%X\n", hdr.Command, len(reqBody), hdr.Status, hdr.SessionID)
		respHdr, respBody := s.processCommand(state, hdr, reqBody)
		if respHdr != nil {
			s.sendPacket(conn, *respHdr, respBody)
		}
	}
}

func (s *Server) processCommand(state *connState, reqHdr Header, reqBody []byte) (*Header, []byte) {
	respHdr := reqHdr
	respHdr.Flags = 0x00000001 // SMB2_FLAGS_SERVER_TO_REDIR
	fmt.Fprintf(os.Stderr, "DEBUG processCommand: cmd=0x%04X bodyLen=%d\n", reqHdr.Command, len(reqBody))

	switch reqHdr.Command {
	case CommandNegotiate:
		// SMB2 Negotiate Response Structure (MS-SMB2 2.2.4 - 65 bytes)
		respHdr.Status = 0x00000000 // STATUS_SUCCESS
		respHdr.CreditReqResp = 1

		body := make([]byte, 65)
		binary.LittleEndian.PutUint16(body[0:2], 65)     // StructureSize (must be 65)
		binary.LittleEndian.PutUint16(body[2:4], 0)      // SecurityMode
		binary.LittleEndian.PutUint16(body[4:6], 0x0210) // DialectRevision (SMB 2.10)
		binary.LittleEndian.PutUint16(body[6:8], 0)      // NegotiateContextCount/Reserved
		binary.LittleEndian.PutUint32(body[28:32], 65536) // MaxTransactSize (64KB)
		binary.LittleEndian.PutUint32(body[32:36], 65536) // MaxReadSize (64KB)
		binary.LittleEndian.PutUint32(body[36:40], 65536) // MaxWriteSize (64KB)
		return &respHdr, body

	case CommandSessionSetup:
		// Immediate Session Setup Success (Guest / Standard)
		respHdr.Status = 0x00000000
		respHdr.SessionID = 0x100000001

		body := make([]byte, 9)
		binary.LittleEndian.PutUint16(body[0:2], 9) // StructureSize (9)
		binary.LittleEndian.PutUint16(body[2:4], 1) // SessionFlags (GUEST)
		binary.LittleEndian.PutUint16(body[4:6], 0)
		binary.LittleEndian.PutUint16(body[6:8], 0)
		body[8] = 0
		return &respHdr, body

	case CommandTreeConnect:
		respHdr.Status = 0x00000000
		respHdr.TreeID = 1

		// Detect IPC$ share (used for NetShareEnumAll / srvsvc named pipe)
		var isIPC bool
		if len(reqBody) >= 4 {
			pathOffset := binary.LittleEndian.Uint16(reqBody[2:4])
			pathLen := binary.LittleEndian.Uint16(reqBody[4:6])
			if int(pathOffset) >= 64 {
				pathStart := int(pathOffset) - 64
				pathEnd := pathStart + int(pathLen)
				if pathEnd <= len(reqBody) {
					rawPath := reqBody[pathStart:pathEnd]
					// Decode UTF-16LE path
					var path string
					for i := 0; i+1 < len(rawPath); i += 2 {
						ch := rune(binary.LittleEndian.Uint16(rawPath[i : i+2]))
						path += string(ch)
					}
					if len(path) >= 4 && path[len(path)-4:] == "IPC$" {
						isIPC = true
					}
				}
			}
		}

		state.nextHandleID++
		treeID := uint32(state.nextHandleID)
		respHdr.TreeID = treeID
		if isIPC {
			state.ipcTrees[treeID] = true
			body := make([]byte, 16)
			binary.LittleEndian.PutUint16(body[0:2], 16)
			body[2] = 0x02 // ShareType: PIPE (IPC$)
			return &respHdr, body
		}

		body := make([]byte, 16)
		binary.LittleEndian.PutUint16(body[0:2], 16) // Struct Size
		body[2] = 0x01                               // ShareType: DISK
		return &respHdr, body

	case CommandTreeDisconn:
		delete(state.ipcTrees, reqHdr.TreeID)
		respHdr.Status = 0x00000000
		body := make([]byte, 4)
		binary.LittleEndian.PutUint16(body[0:2], 4)
		return &respHdr, body

	case CommandCreate:
		// Increment handle ID per open call
		state.nextHandleID++
		fileHandleID := state.nextHandleID
		state.handles[fileHandleID] = &handleState{cursor: 0, entries: nil}

		respHdr.Status = 0x00000000
		body := make([]byte, 89)
		binary.LittleEndian.PutUint16(body[0:2], 89)
		// File ID Persistent (64:72) & Volatile (72:80)
		binary.LittleEndian.PutUint64(body[64:72], fileHandleID)
		binary.LittleEndian.PutUint64(body[72:80], fileHandleID)
		return &respHdr, body

	case CommandClose:
		if len(reqBody) >= 24 {
			handleID := binary.LittleEndian.Uint64(reqBody[8:16])
			delete(state.handles, handleID)
		}
		respHdr.Status = 0x00000000
		body := make([]byte, 60)
		binary.LittleEndian.PutUint16(body[0:2], 60)
		return &respHdr, body

	case CommandWrite:
		// SMB2 Write Request
		respHdr.Status = 0x00000000
		body := make([]byte, 16)
		binary.LittleEndian.PutUint16(body[0:2], 17) // Struct Size (17 bytes)
		if len(reqBody) >= 16 {
			dataLen := binary.LittleEndian.Uint32(reqBody[4:8])
			binary.LittleEndian.PutUint32(body[4:8], dataLen) // Count written bytes
		}
		return &respHdr, body

	case CommandIoctl:
		// SMB2_IOCTL (0x000B) - MS-SMB2 2.2.31
		// GVFS uses FSCTL_PIPE_TRANSCEIVE (0x0011C017) over IPC$/\pipe\srvsvc to enumerate shares.
		// The payload is a DCE/RPC packet. We handle:
		//   - RPC Bind  (type 0x0B): respond with Bind_Ack (accepting SRVSVC interface)
		//   - RPC Request (type 0x00, opnum 15 = NetShareEnumAll): respond with share list
		respHdr.Status = 0x00000000

		// SMB2_IOCTL Request layout (body offsets, per MS-SMB2 2.2.31):
		// [0:2]   StructureSize (57)
		// [2:4]   Reserved
		// [4:8]   CtlCode
		// [8:24]  FileId
		// [24:28] InputOffset  (offset from start of SMB2 header)
		// [28:32] InputCount
		// [32:36] MaxInputResponse
		// [36:40] OutputOffset
		// [40:44] OutputCount
		// [44:48] MaxOutputResponse
		// [48:52] Flags
		// [52:56] Reserved2
		var ctlCode uint32
		if len(reqBody) >= 8 {
			ctlCode = binary.LittleEndian.Uint32(reqBody[4:8])
		}
		n60 := len(reqBody)
		if n60 > 60 {
			n60 = 60
		}
		fmt.Fprintf(os.Stderr, "DEBUG IOCTL: CtlCode=0x%08X bodyLen=%d bodyHead=%X\n", ctlCode, len(reqBody), reqBody[:n60])

		if ctlCode == 0x0011C017 { // FSCTL_PIPE_TRANSCEIVE
			// Parse InputOffset and InputCount
			var inputOffset, inputCount uint32
			if len(reqBody) >= 32 {
				inputOffset = binary.LittleEndian.Uint32(reqBody[24:28])
				inputCount = binary.LittleEndian.Uint32(reqBody[28:32])
			}
			// InputOffset is relative to start of SMB2 header (64 bytes before body)
			rpcStart := int(inputOffset) - 64
			rpcEnd := rpcStart + int(inputCount)
			var rpcData []byte
			if rpcStart >= 0 && rpcEnd <= len(reqBody) && inputCount > 0 {
				rpcData = reqBody[rpcStart:rpcEnd]
			}

			// DCE/RPC packet type is at byte 2 of RPC header
			rpcType := byte(0xFF)
			if len(rpcData) >= 3 {
				rpcType = rpcData[2]
			}
			// Call ID at bytes 12:16
			var callID uint32
			if len(rpcData) >= 16 {
				callID = binary.LittleEndian.Uint32(rpcData[12:16])
			}
			fmt.Fprintf(os.Stderr, "DEBUG RPC: type=0x%02X callID=%d rpcLen=%d\n", rpcType, callID, len(rpcData))

			var ndrData []byte
			switch rpcType {
			case 0x0B: // DCE/RPC Bind - respond with Bind_Ack accepting SRVSVC
				// DCE/RPC common header layout:
				//  0: rpc_vers (5)
				//  1: rpc_vers_minor (0)
				//  2: PTYPE (0x0C = Bind_Ack)
				//  3: pfc_flags
				//  4: packed_drep[4] (10 00 00 00 = little-endian)
				//  8: frag_length (uint16)
				// 10: auth_length (uint16)
				// 12: call_id (uint32)
				// 16: max_xmit_frag (uint16)
				// 18: max_recv_frag (uint16)
				// 20: assoc_group_id (uint32)
				// 24: sec_addr (2-byte len + string + pad)
				// then: p_results
				bindAck := []byte{
					0x05, 0x00,             // rpc_vers, rpc_vers_minor
					0x0C,                   // PTYPE: Bind_Ack
					0x03,                   // pfc_flags: first_frag | last_frag
					0x10, 0x00, 0x00, 0x00, // packed_drep: little-endian
					0x00, 0x00,             // frag_length (set below)
					0x00, 0x00,             // auth_length = 0
					0x00, 0x00, 0x00, 0x00, // call_id (set from request)
					0xB8, 0x10,             // max_xmit_frag = 4280
					0xB8, 0x10,             // max_recv_frag = 4280
					0x41, 0x00, 0x00, 0x00, // assoc_group_id
					// Secondary address: "\PIPE\srvsvc" (13 bytes + null)
					0x0D, 0x00,             // sec_addr length
					0x5C, 0x50, 0x49, 0x50, 0x45, 0x5C,
					0x73, 0x72, 0x76, 0x73, 0x76, 0x63, 0x00,
					0x00,                   // pad to 4-byte alignment
					// p_result_list: num_results=1
					0x01, 0x00,             // num_results
					0x00, 0x00,             // pad
					// p_result[0]: result=acceptance(0), reason=reason_not_specified(0)
					0x00, 0x00,             // result: acceptance
					0x00, 0x00,             // reason
					// Transferred syntax: NDR {8a885d04-1ceb-11c9-9fe8-08002b104860} v2
					0x04, 0x5D, 0x88, 0x8A, 0xEB, 0x1C, 0xC9, 0x11,
					0x9F, 0xE8, 0x08, 0x00, 0x2B, 0x10, 0x48, 0x60,
					0x02, 0x00, 0x00, 0x00, // syntax version 2
				}
				// Set call_id (bytes 12:16)
				binary.LittleEndian.PutUint32(bindAck[12:16], callID)
				// Set frag_length (bytes 8:10)
				binary.LittleEndian.PutUint16(bindAck[8:10], uint16(len(bindAck)))
				ndrData = bindAck

			case 0x00: // DCE/RPC Request
				// opnum is at bytes 22:24
				var opnum uint16
				if len(rpcData) >= 24 {
					opnum = binary.LittleEndian.Uint16(rpcData[22:24])
				}
				// alloc_hint at 16:20, stub data starts at 24
				stubIn := rpcData
				if len(rpcData) >= 24 {
					stubIn = rpcData[24:]
				}
				n80 := len(stubIn)
				if n80 > 80 {
					n80 = 80
				}
				fmt.Fprintf(os.Stderr, "DEBUG RPC Request: opnum=%d stubLen=%d stubHead=%X\n", opnum, len(stubIn), stubIn[:n80])

				// Build NetShareEnumAll Level=1 NDR stub response
				// This encoding matches Samba's NDR32 output (verified with samba Python bindings):
				// - Uses Samba-specific pointer layout tokens (uint16 offset, uint16 0x0002)
				// - Each SHARE_INFO_1 entry: name_ptr(token)+type(uint32)+comment_ptr(token)
				// - String data deferred per entry, aligned to 4 bytes
				stub := new(bytes.Buffer)

				// Helper: write a Samba NDR pointer token: uint16(seq*4) + uint16(0x0002)
				writePtrToken := func(seq int) {
					binary.Write(stub, binary.LittleEndian, uint16(seq*4))
					binary.Write(stub, binary.LittleEndian, uint16(0x0002))
				}

				// Helper: write a conformant varying WSTR (LPWSTR target)
				writeString := func(s string) {
					// Convert to UTF-16LE including null terminator
					runes := []rune(s)
					utf16Data := utf16.Encode(append(runes, 0))
					count := len(utf16Data) // includes null
					// Conformant varying array header: max_count, offset, actual_count
					binary.Write(stub, binary.LittleEndian, uint32(count)) // max_count
					binary.Write(stub, binary.LittleEndian, uint32(0))     // offset
					binary.Write(stub, binary.LittleEndian, uint32(count)) // actual_count
					// UTF-16LE data
					for _, r := range utf16Data {
						binary.Write(stub, binary.LittleEndian, r)
					}
					// Align to 4 bytes (NDR32 alignment)
					for stub.Len()%4 != 0 {
						stub.WriteByte(0)
					}
				}

				// Define shares to enumerate (dynamic, from active shares list)
				type shareEntry struct {
					Name    string
					Type    uint32
					Comment string
				}
				shares := []shareEntry{
					{Name: "share", Type: 0, Comment: ""},
				}
				numShares := len(shares)

				// SHARE_ENUM_STRUCT (embedded via [ref] pointer — no referent ID at top level):
				//   Level (DWORD) = 1
				//   [switch_is(Level)] SHARE_ENUM_UNION ctr
				binary.Write(stub, binary.LittleEndian, uint32(1)) // Level = 1

				// Union arm [case(1)]: SHARE_ENUM_CTR1 *ctr1 — unique pointer
				// Inline: referent ID (simple uint32, non-zero for non-null)
				binary.Write(stub, binary.LittleEndian, uint32(1)) // ctr1 referent ID

				// Deferred ctr1 data:
				// Samba NDR adds a pointer layout descriptor before the struct
				writePtrToken(0)                                     // pointer layout header
				binary.Write(stub, binary.LittleEndian, uint32(numShares)) // count
				writePtrToken(1)                                     // array pointer (SHARE_INFO_1*)
				binary.Write(stub, binary.LittleEndian, uint32(numShares)) // conformant array max_count

				// SHARE_INFO_1 array entries (inline pointers + type)
				// Each entry: name_ptr(token) + type(uint32) + comment_ptr(token)
				for i := 0; i < numShares; i++ {
					writePtrToken(2 + i*2)                             // shi1_netname pointer (name)
					binary.Write(stub, binary.LittleEndian, shares[i].Type) // shi1_type
					writePtrToken(3 + i*2)                             // shi1_remark pointer (comment)
				}

				// Deferred string data per entry (interleaved: name then comment per entry)
				for _, s := range shares {
					writeString(s.Name)
					writeString(s.Comment)
				}

				// Total entries (DWORD*) — [ref] pointer, inline value
				binary.Write(stub, binary.LittleEndian, uint32(numShares))
				// Resume handle ([in,out,unique] DWORD*) — NULL (no more entries)
				binary.Write(stub, binary.LittleEndian, uint32(0))
				// Return code (WERROR)
				binary.Write(stub, binary.LittleEndian, uint32(0))

				stubBytes := stub.Bytes()
				// Build RPC Response header (24 bytes) per DCE/RPC spec:
				//  0: rpc_vers(5), rpc_vers_minor(0)
				//  2: PTYPE(0x02=Response), pfc_flags(0x03)
				//  4: packed_drep[4] (10 00 00 00)
				//  8: frag_length (uint16)
				// 10: auth_length (uint16)
				// 12: call_id (uint32)
				// 16: alloc_hint (uint32)
				// 20: p_cont_id (uint16)
				// 22: cancel_count / reserved (uint16)
				// 24: stub data
				rpcResp := make([]byte, 24+len(stubBytes))
				rpcResp[0] = 0x05 // rpc_vers
				rpcResp[1] = 0x00 // rpc_vers_minor
				rpcResp[2] = 0x02 // PTYPE: Response
				rpcResp[3] = 0x03 // pfc_flags: first_frag | last_frag
				rpcResp[4] = 0x10 // packed_drep[0]: little-endian
				// [5:8] = 0 (rest of packed_drep)
				binary.LittleEndian.PutUint16(rpcResp[8:10], uint16(len(rpcResp)))  // frag_length
				// auth_length = 0
				binary.LittleEndian.PutUint32(rpcResp[12:16], callID) // call_id
				binary.LittleEndian.PutUint32(rpcResp[16:20], uint32(len(stubBytes))) // alloc_hint
				// p_cont_id=0, cancel_count=0 (already zero)
				copy(rpcResp[24:], stubBytes)
				ndrData = rpcResp

			default:
				fmt.Fprintf(os.Stderr, "DEBUG RPC: unknown type=0x%02X, returning empty response\n", rpcType)
				ndrData = []byte{}
			}

			// Build SMB2 IOCTL Response (MS-SMB2 2.2.32)
			// Fixed IOCTL response header is 48 bytes (StructureSize + fields)
			// OutputOffset = 64 (SMB2 header) + 48 (IOCTL resp fixed) = 112
			const ioctlRespFixed = 48
			const outputOffset = 64 + ioctlRespFixed
			body := make([]byte, ioctlRespFixed+len(ndrData))
			binary.LittleEndian.PutUint16(body[0:2], 49) // StructureSize = 49
			// body[2:4] = Reserved (zero)
			binary.LittleEndian.PutUint32(body[4:8], ctlCode) // CtlCode
			// body[8:24] = FileId (zero)
			// InputOffset and InputCount = 0 in response
			if len(ndrData) > 0 {
				binary.LittleEndian.PutUint32(body[32:36], outputOffset)           // OutputOffset
				binary.LittleEndian.PutUint32(body[36:40], uint32(len(ndrData)))  // OutputCount
			}
			// Flags = 0, Reserved2 = 0
			n80 := len(ndrData)
			if n80 > 80 {
				n80 = 80
			}
			fmt.Fprintf(os.Stderr, "DEBUG IOCTL resp: ndrLen=%d ndrHead=%X\n", len(ndrData), ndrData[:n80])
			copy(body[ioctlRespFixed:], ndrData)
			return &respHdr, body
		}

		// Unknown IOCTL - return STATUS_NOT_SUPPORTED
		respHdr.Status = 0xC00000BB
		body := make([]byte, 49)
		binary.LittleEndian.PutUint16(body[0:2], 49)
		return &respHdr, body

	case 0x000E: // SMB2_QUERY_DIRECTORY
		// reqBody[2] is FileInformationClass
		infoClass := byte(0x25)
		if len(reqBody) > 2 {
			infoClass = reqBody[2]
		}

		// SMB2_QUERY_DIRECTORY body structure (MS-SMB2 2.2.33):
		// 0..2: StructureSize (33)
		// 2..3: FileInformationClass
		// 3..4: Flags
		// 4..8: FileIndex
		// 8..24: FileId (16 bytes: 8 bytes Persistent, 8 bytes Volatile)
		// Or if offsets shift, check both 8..16 and 24..32:
		var handleID uint64 = 1
		if len(reqBody) >= 24 {
			handleID = binary.LittleEndian.Uint64(reqBody[8:16])
			if handleID == 0 && len(reqBody) >= 32 {
				handleID = binary.LittleEndian.Uint64(reqBody[24:32])
			}
		}
		if handleID == 0 {
			handleID = 1
		}

		hState, exists := state.handles[handleID]
		if !exists {
			hState = &handleState{cursor: 0}
			state.handles[handleID] = hState
		}

		// Check flags in SMB2_QUERY_DIRECTORY request body
		// 0x01 = SMB2_RESTART_SCANS, 0x02 = SMB2_RETURN_SINGLE_ENTRY
		var flags uint8 = 0
		if len(reqBody) >= 3 {
			flags = reqBody[2]
		}

		if hState.hasQueried {
			respHdr.Status = 0x80000006 // STATUS_NO_MORE_FILES
			body := make([]byte, 9)
			binary.LittleEndian.PutUint16(body[0:2], 9) // StructureSize
			body[2] = 0                                 // BufferOffset / Reserved
			return &respHdr, body
		}

		if flags&0x01 != 0 || hState.entries == nil {
			entries, err := os.ReadDir(s.cfg.ShareDir)
			if err != nil {
				respHdr.Status = 0x80000006 // STATUS_NO_MORE_FILES
				body := make([]byte, 9)
				binary.LittleEndian.PutUint16(body[0:2], 9)
				return &respHdr, body
			}
			hState.entries = entries
			hState.cursor = 0
		}

		if hState.cursor >= len(hState.entries) {
			hState.hasQueried = true
			respHdr.Status = 0x80000006 // STATUS_NO_MORE_FILES
			body := make([]byte, 9)
			binary.LittleEndian.PutUint16(body[0:2], 9) // StructureSize
			body[2] = 0                                 // BufferOffset / Reserved
			return &respHdr, body
		}

		batch := hState.entries[hState.cursor:]
		if flags&0x02 != 0 && len(batch) > 1 {
			batch = batch[:1]
		}
		hState.cursor += len(batch)

		respHdr.Status = 0x00000000 // STATUS_SUCCESS
		var dirBuf bytes.Buffer

		for i, entry := range batch {
			info, _ := entry.Info()
			var size int64
			var fileAttrs uint32 = 0x20 // FILE_ATTRIBUTE_ARCHIVE
			if entry.IsDir() {
				fileAttrs = 0x10 // FILE_ATTRIBUTE_DIRECTORY
			} else if info != nil {
				size = info.Size()
			}

			// Encode file name in UTF-16LE
			nameRunes := []rune(entry.Name())
			nameUTF16 := make([]byte, len(nameRunes)*2)
			for idx, r := range nameRunes {
				binary.LittleEndian.PutUint16(nameUTF16[idx*2:idx*2+2], uint16(r))
			}
			nameLen := len(nameUTF16)

			recStart := dirBuf.Len()
			dirBuf.Write(make([]byte, 4))                              // NextEntryOffset (uint32)
			dirBuf.Write(make([]byte, 4))                              // FileIndex (uint32)
			_ = binary.Write(&dirBuf, binary.LittleEndian, uint64(0)) // CreationTime (int64)
			_ = binary.Write(&dirBuf, binary.LittleEndian, uint64(0)) // LastAccessTime (int64)
			_ = binary.Write(&dirBuf, binary.LittleEndian, uint64(0)) // LastWriteTime (int64)
			_ = binary.Write(&dirBuf, binary.LittleEndian, uint64(0)) // ChangeTime (int64)
			_ = binary.Write(&dirBuf, binary.LittleEndian, size)      // EndOfFile (int64)
			_ = binary.Write(&dirBuf, binary.LittleEndian, size)      // AllocationSize (int64)
			_ = binary.Write(&dirBuf, binary.LittleEndian, fileAttrs) // FileAttributes (uint32)
			_ = binary.Write(&dirBuf, binary.LittleEndian, uint32(nameLen)) // FileNameLength (uint32)

			if infoClass == 0x25 {
				_ = binary.Write(&dirBuf, binary.LittleEndian, uint32(0)) // EA Size (uint32)
				dirBuf.Write([]byte{0, 0})                                 // ShortNameLength (1) + Reserved (1)
				dirBuf.Write(make([]byte, 24))                             // ShortName (24)
				_ = binary.Write(&dirBuf, binary.LittleEndian, uint16(0)) // Reserved2 (uint16)
				_ = binary.Write(&dirBuf, binary.LittleEndian, uint64(i+1))// FileID (uint64)
			}

			dirBuf.Write(nameUTF16) // FileName bytes

			entryLen := dirBuf.Len() - recStart
			padLen := (8 - (entryLen % 8)) % 8
			if padLen > 0 {
				dirBuf.Write(make([]byte, padLen))
				entryLen += padLen
			}

			// Update NextEntryOffset (0 if last entry in batch)
			if i < len(batch)-1 {
				binary.LittleEndian.PutUint32(dirBuf.Bytes()[recStart:recStart+4], uint32(entryLen))
			} else {
				binary.LittleEndian.PutUint32(dirBuf.Bytes()[recStart:recStart+4], 0)
			}
		}

		hState.hasQueried = true

		outputBytes := dirBuf.Bytes()
		outputLen := len(outputBytes)

		body := make([]byte, 8+outputLen)
		binary.LittleEndian.PutUint16(body[0:2], 9)                 // StructureSize (9)
		binary.LittleEndian.PutUint16(body[2:4], 72)                // OutputBufferOffset (64 header + 8 body header = 72)
		binary.LittleEndian.PutUint32(body[4:8], uint32(outputLen)) // OutputBufferLength
		copy(body[8:], outputBytes)

		return &respHdr, body

	case CommandQueryInfo, 0x0012:
		// SMB2_QUERY_INFO Request (MS-SMB2 2.2.37):
		// reqBody[0..2]: StructureSize (41 = 0x0029)
		// reqBody[2]: InfoType (0x01 File, 0x02 Filesystem, 0x03 Security, 0x04 Quota)
		// reqBody[3]: FileInfoClass / FsInfoClass
		respHdr.Status = 0x00000000 // STATUS_SUCCESS

		var infoType byte = 0x02
		var infoClass byte = 0x03
		if len(reqBody) >= 4 {
			infoType = reqBody[2]
			infoClass = reqBody[3]
		}

		var infoBuf []byte
		if infoType == 0x01 {
			// File Information Queries (MS-FSCC 2.4)
			switch infoClass {
			case 0x04: // FileBasicInformation (40 bytes - MS-FSCC 2.4.7)
				infoBuf = make([]byte, 40)
				// CreationTime (0..8), LastAccessTime (8..16), LastWriteTime (16..24), ChangeTime (24..32)
				binary.LittleEndian.PutUint32(infoBuf[32:36], 0x10) // FileAttributes: DIRECTORY
			case 0x05: // FileStandardInformation (24 bytes - MS-FSCC 2.4.38)
				infoBuf = make([]byte, 24)
				binary.LittleEndian.PutUint64(infoBuf[0:8], 4096)  // AllocationSize
				binary.LittleEndian.PutUint64(infoBuf[8:16], 4096) // EndOfFile
				binary.LittleEndian.PutUint32(infoBuf[16:20], 1)   // NumberOfLinks
				infoBuf[20] = 0                                    // DeletePending
				infoBuf[21] = 1                                    // Directory
			case 0x12, 0x00: // FileAllInformation (100 bytes - MS-FSCC 2.4.8)
				infoBuf = make([]byte, 100)
				// BasicInfo (40 bytes)
				binary.LittleEndian.PutUint32(infoBuf[32:36], 0x10) // Attributes: DIRECTORY
				// StandardInfo (24 bytes at offset 40)
				binary.LittleEndian.PutUint64(infoBuf[40:48], 4096) // AllocationSize
				binary.LittleEndian.PutUint64(infoBuf[48:56], 4096) // EndOfFile
				binary.LittleEndian.PutUint32(infoBuf[56:60], 1)    // NumberOfLinks
				infoBuf[60] = 0                                     // DeletePending
				infoBuf[61] = 1                                     // Directory
			default:
				// Default FileBasicInformation (40 bytes)
				infoBuf = make([]byte, 40)
				binary.LittleEndian.PutUint32(infoBuf[32:36], 0x10) // Attributes: DIRECTORY
			}
		} else if infoType == 0x02 && infoClass == 0x03 {
			// FileFsSizeInformation (24 bytes - MS-FSCC 2.5.8):
			infoBuf = make([]byte, 24)
			binary.LittleEndian.PutUint64(infoBuf[0:8], 100000000)  // TotalAllocationUnits
			binary.LittleEndian.PutUint64(infoBuf[8:16], 80000000)  // AvailableAllocationUnits
			binary.LittleEndian.PutUint32(infoBuf[16:20], 8)        // SectorsPerAllocationUnit
			binary.LittleEndian.PutUint32(infoBuf[20:24], 512)      // BytesPerSector
		} else if infoType == 0x02 && infoClass == 0x07 {
			// FileFsFullSizeInformation (32 bytes - MS-FSCC 2.5.4)
			infoBuf = make([]byte, 32)
			binary.LittleEndian.PutUint64(infoBuf[0:8], 100000000)  // TotalAllocationUnits
			binary.LittleEndian.PutUint64(infoBuf[8:16], 80000000)  // CallerAvailableAllocationUnits
			binary.LittleEndian.PutUint64(infoBuf[16:24], 80000000) // ActualAvailableAllocationUnits
			binary.LittleEndian.PutUint32(infoBuf[24:28], 8)        // SectorsPerAllocationUnit
			binary.LittleEndian.PutUint32(infoBuf[28:32], 512)      // BytesPerSector
		} else if infoType == 0x02 && infoClass == 0x01 {
			// FileFsVolumeInformation (MS-FSCC 2.5.9 - 18 bytes + UTF16 label)
			infoBuf = make([]byte, 24)
			binary.LittleEndian.PutUint64(infoBuf[0:8], 0)            // VolumeCreationTime
			binary.LittleEndian.PutUint32(infoBuf[8:12], 0x12345678)  // VolumeSerialNumber
			binary.LittleEndian.PutUint32(infoBuf[12:16], 6)          // VolumeLabelLength
			infoBuf[16] = 0                                           // SupportsObjects
			infoBuf[17] = 0                                           // Reserved
			copy(infoBuf[18:], []byte{'N', 0, 'A', 0, 'S', 0})       // Label "NAS"
		} else if infoType == 0x02 && infoClass == 0x05 {
			// FileFsAttributeInformation (MS-FSCC 2.5.1 - 12 bytes + UTF16 FsName)
			infoBuf = make([]byte, 20)
			binary.LittleEndian.PutUint32(infoBuf[0:4], 0x00000001) // FileSystemAttributes
			binary.LittleEndian.PutUint32(infoBuf[4:8], 255)        // MaximumComponentNameLength
			binary.LittleEndian.PutUint32(infoBuf[8:12], 8)         // FileSystemNameLength
			copy(infoBuf[12:], []byte{'N', 0, 'T', 0, 'F', 0, 'S', 0})
		} else {
			// Default FileFsSizeInformation (24 bytes)
			infoBuf = make([]byte, 24)
			binary.LittleEndian.PutUint64(infoBuf[0:8], 100000000)  // TotalAllocationUnits
			binary.LittleEndian.PutUint64(infoBuf[8:16], 80000000)  // AvailableAllocationUnits
			binary.LittleEndian.PutUint32(infoBuf[16:20], 8)        // SectorsPerAllocationUnit
			binary.LittleEndian.PutUint32(infoBuf[20:24], 512)      // BytesPerSector
		}

		body := make([]byte, 8+len(infoBuf))
		binary.LittleEndian.PutUint16(body[0:2], 9)                   // StructureSize (9)
		binary.LittleEndian.PutUint16(body[2:4], 72)                  // OutputBufferOffset (72)
		binary.LittleEndian.PutUint32(body[4:8], uint32(len(infoBuf)))  // OutputBufferLength
		copy(body[8:], infoBuf)
		return &respHdr, body

	default:
		// Return STATUS_NOT_SUPPORTED for unimplemented features
		respHdr.Status = 0xC00000BB
		return &respHdr, []byte{4, 0}
	}
}

func (s *Server) sendPacket(conn net.Conn, hdr Header, body []byte) {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, hdr)
	buf.Write(body)

	packetLen := buf.Len()
	netbiosHdr := []byte{
		0x00,
		byte(packetLen >> 16),
		byte(packetLen >> 8),
		byte(packetLen),
	}

	_, _ = conn.Write(netbiosHdr)
	_, _ = conn.Write(buf.Bytes())
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}
