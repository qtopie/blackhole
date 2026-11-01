package nfs

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// CompoundState manages per-connection or per-compound context.
type CompoundState struct {
	CFH []byte // Current Filehandle
	SFH []byte // Saved Filehandle
}

type ClientState struct {
	ClientID uint64
	Verifier [8]byte
	Confirm  [8]byte
	ClientStr string
}

type NFSHandler struct {
	rootDir   string
	hm        *HandleManager
	lm        *LockManager
	sm        *SessionManager
	clientsMu sync.Mutex
	clients   map[uint64]*ClientState
	nextCID   uint64
	bootTime  time.Time
}

func NewNFSHandler(rootDir string, hm *HandleManager) *NFSHandler {
	return &NFSHandler{
		rootDir:  rootDir,
		hm:       hm,
		lm:       NewLockManager(),
		sm:       NewSessionManager(),
		clients:  make(map[uint64]*ClientState),
		nextCID:  1,
		bootTime: time.Now(),
	}
}

// ProcessCompound executes a batched COMPOUND request according to RFC 7530.
func (h *NFSHandler) ProcessCompound(reader *XDRReader) ([]byte, error) {
	tag, err := reader.ReadString()
	if err != nil {
		return nil, fmt.Errorf("read tag failed: %w", err)
	}
	minorVer, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("read minorversion failed: %w", err)
	}
	if minorVer > 2 {
		writer := NewXDRWriter()
		writer.WriteUint32(NFS4ERRMINORVERSMISMATCH)
		writer.WriteString(tag)
		writer.WriteUint32(0)
		return writer.Bytes(), nil
	}
	argCount, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("read argarray count failed: %w", err)
	}

	state := &CompoundState{}
	writer := NewXDRWriter()

	// Reserve space for Compound Header: Status (4B), Tag, ResCount (4B)
	// We will fill Status and ResCount after processing.
	resOpsWriter := NewXDRWriter()
	compoundStatus := NFS4OK
	executedOps := uint32(0)

	for i := uint32(0); i < argCount; i++ {
		opNum, err := reader.ReadUint32()
		if err != nil {
			return nil, fmt.Errorf("read op num at %d failed: %w", i, err)
		}

		opStatus := h.dispatchOp(opNum, reader, resOpsWriter, state, minorVer)
		executedOps++

		if opStatus != NFS4OK {
			compoundStatus = opStatus
			break // RFC 7530: Stop on first error
		}
	}

	// Write top-level compound reply
	writer.WriteUint32(compoundStatus)
	writer.WriteString(tag)
	writer.WriteUint32(executedOps)
	writer.WriteRaw(resOpsWriter.Bytes())

	return writer.Bytes(), nil
}

func (h *NFSHandler) dispatchOp(opNum uint32, reader *XDRReader, out *XDRWriter, state *CompoundState, minorVer uint32) uint32 {
	out.WriteUint32(opNum) // resop4 matches argop4

	switch opNum {
	case OpPutrootfh:
		state.CFH = h.hm.RootFH()
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpPutfh:
		fh, err := reader.ReadBytes()
		if err != nil {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		if _, _, ok := h.hm.ResolvePath(fh); !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		state.CFH = fh
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpGetfh:
		if len(state.CFH) == 0 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		out.WriteUint32(NFS4OK)
		out.WriteBytes(state.CFH)
		return NFS4OK

	case OpSavefh:
		if len(state.CFH) == 0 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		state.SFH = state.CFH
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpRestorefh:
		if len(state.SFH) == 0 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		state.CFH = state.SFH
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpLookup:
		name, err := reader.ReadString()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		rel, abs, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		fi, err := os.Lstat(abs)
		if err != nil {
			out.WriteUint32(NFS4ERRNOENT)
			return NFS4ERRNOENT
		}
		if !fi.IsDir() {
			out.WriteUint32(NFS4ERRNOTDIR)
			return NFS4ERRNOTDIR
		}

		childRel := filepath.Clean(filepath.Join(rel, name))
		childAbs := filepath.Join(h.rootDir, childRel)
		if _, err := os.Lstat(childAbs); err != nil {
			out.WriteUint32(NFS4ERRNOENT)
			return NFS4ERRNOENT
		}

		state.CFH = h.hm.GetOrCreateFH(childRel)
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpLookupp:
		rel, _, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		parentRel := filepath.Dir(rel)
		if rel == "" || parentRel == "." {
			parentRel = ""
		}
		state.CFH = h.hm.GetOrCreateFH(parentRel)
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpAccess:
		reqAccess, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		if len(state.CFH) == 0 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		out.WriteUint32(NFS4OK)
		supported := Access4Read | Access4Lookup | Access4Modify | Access4Extend | Access4Delete | Access4Execute
		granted := reqAccess & supported
		out.WriteUint32(supported)
		out.WriteUint32(granted)
		return NFS4OK

	case OpGetattr:
		bm, err := reader.ReadBitmap()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		rel, abs, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		fi, err := os.Lstat(abs)
		if err != nil {
			out.WriteUint32(NFS4ERRNOENT)
			return NFS4ERRNOENT
		}

		out.WriteUint32(NFS4OK)
		h.encodeAttributes(out, bm, fi, rel, state.CFH)
		return NFS4OK

	case OpSetclientid:
		var verifier [8]byte
		v, err := reader.ReadFixedBytes(8)
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		copy(verifier[:], v)
		clientStr, err := reader.ReadString()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		// callback info (skip program, netid, uaddr, ident)
		_, _ = reader.ReadUint32()
		_, _ = reader.ReadString()
		_, _ = reader.ReadString()
		_, _ = reader.ReadUint32()

		h.clientsMu.Lock()
		cid := h.nextCID
		h.nextCID++
		var confirm [8]byte
		_, _ = rand.Read(confirm[:])
		h.clients[cid] = &ClientState{
			ClientID:  cid,
			Verifier:  verifier,
			Confirm:   confirm,
			ClientStr: clientStr,
		}
		h.clientsMu.Unlock()

		out.WriteUint32(NFS4OK)
		out.WriteUint64(cid)
		out.WriteFixedBytes(confirm[:])
		return NFS4OK

	case OpSetclientidConf:
		cid, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		confirm, err := reader.ReadFixedBytes(8)
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		h.clientsMu.Lock()
		client, exists := h.clients[cid]
		h.clientsMu.Unlock()
		if !exists {
			out.WriteUint32(NFS4ERRSTALE)
			return NFS4ERRSTALE
		}
		for i := 0; i < 8; i++ {
			if client.Confirm[i] != confirm[i] {
				out.WriteUint32(NFS4ERRCLIDINUSE)
				return NFS4ERRCLIDINUSE
			}
		}
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpRenew:
		_, _ = reader.ReadUint64()
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpSecinfo:
		_, _ = reader.ReadString() // objname
		out.WriteUint32(NFS4OK)
		out.WriteUint32(1)              // count = 1
		out.WriteUint32(AuthFlavorSys) // flavor = AUTH_SYS
		return NFS4OK

	case OpReaddir:
		cookie, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		cookieVerf, err := reader.ReadFixedBytes(8)
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		_, _ = reader.ReadUint32() // dircount
		_, _ = reader.ReadUint32() // maxcount
		reqAttrs, err := reader.ReadBitmap()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}

		rel, abs, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		entries, err := os.ReadDir(abs)
		if err != nil {
			out.WriteUint32(NFS4ERRNOENT)
			return NFS4ERRNOENT
		}

		out.WriteUint32(NFS4OK)
		out.WriteFixedBytes(cookieVerf) // echo cookieverf

		// Write entries
		startIndex := int(cookie)
		for i := startIndex; i < len(entries); i++ {
			entry := entries[i]
			out.WriteUint32(1) // has_next = true
			out.WriteUint64(uint64(i + 1))
			out.WriteString(entry.Name())

			entryRel := filepath.Clean(filepath.Join(rel, entry.Name()))
			entryAbs := filepath.Join(h.rootDir, entryRel)
			fi, _ := os.Lstat(entryAbs)
			childFH := h.hm.GetOrCreateFH(entryRel)

			h.encodeAttributes(out, reqAttrs, fi, entryRel, childFH)
		}

		out.WriteUint32(0) // has_next = false
		out.WriteUint32(1) // eof = true
		return NFS4OK

	case OpOpen:
		_, _ = reader.ReadUint32() // seqid
		shareAccess, _ := reader.ReadUint32()
		_, _ = reader.ReadUint32() // shareDeny
		// owner (clientid uint64 + owner string)
		_, _ = reader.ReadUint64()
		_, _ = reader.ReadString()
		openhow, _ := reader.ReadUint32()
		var createMode uint32
		if openhow == 1 { // OPEN4_CREATE
			createMode, _ = reader.ReadUint32()
			switch createMode {
			case 0, 1: // UNCHECKED4, GUARDED4: fattr4
				_ = skipBitmapAttrs(reader)
			case 2: // EXCLUSIVE4: verifier4 (8 bytes)
				_, _ = reader.ReadFixedBytes(8)
			case 3: // EXCLUSIVE4_1: verifier4 (8 bytes) + fattr4
				_, _ = reader.ReadFixedBytes(8)
				_ = skipBitmapAttrs(reader)
			}
		}
		claim, _ := reader.ReadUint32()
		var childName string
		if claim == 0 { // CLAIM_NULL
			childName, _ = reader.ReadString()
		}

		rel, abs, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		targetRel := rel
		targetAbs := abs
		if childName != "" {
			targetRel = filepath.Clean(filepath.Join(rel, childName))
			targetAbs = filepath.Join(h.rootDir, targetRel)
		}

		flags := os.O_RDONLY
		if (shareAccess & 0x02) != 0 {
			flags = os.O_RDWR
		}
		if openhow == 1 {
			flags |= os.O_CREATE
			if createMode == 2 || createMode == 3 {
				flags |= os.O_EXCL
			}
		}

		f, err := os.OpenFile(targetAbs, flags, 0644)
		if err != nil {
			if os.IsNotExist(err) {
				out.WriteUint32(NFS4ERRNOENT)
				return NFS4ERRNOENT
			}
			if os.IsExist(err) {
				out.WriteUint32(NFS4ERREXIST)
				return NFS4ERREXIST
			}
			out.WriteUint32(NFS4ERRACCESS)
			return NFS4ERRACCESS
		}
		_ = f.Close()

		targetFH := h.hm.GetOrCreateFH(targetRel)
		state.CFH = targetFH

		out.WriteUint32(NFS4OK)
		// StateID: 16 bytes (seqid 1 + other 12 bytes)
		var stateID [16]byte
		binary.BigEndian.PutUint32(stateID[0:4], 1)
		copy(stateID[4:16], targetFH[0:12])
		out.WriteFixedBytes(stateID[:])

		// cinfo (change_info4: atomic bool, before uint64, after uint64)
		out.WriteUint32(1) // atomic = true
		out.WriteUint64(uint64(time.Now().UnixNano()))
		out.WriteUint64(uint64(time.Now().UnixNano()))

		out.WriteUint32(0x00000004) // rflags = OPEN4_RESULT_LOCKTYPE_POSIX (0x04)
		// attrset: empty bitmap
		out.WriteBitmap([]uint32{0, 0})
		out.WriteUint32(0) // OPEN_DELEGATE_NONE = 0
		return NFS4OK

	case OpOpenConfirm:
		stateID, err := reader.ReadFixedBytes(16)
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		_, _ = reader.ReadUint32() // seqid
		out.WriteUint32(NFS4OK)
		out.WriteFixedBytes(stateID)
		return NFS4OK

	case OpClose:
		_, _ = reader.ReadUint32() // seqid
		stateID, err := reader.ReadFixedBytes(16)
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		out.WriteUint32(NFS4OK)
		out.WriteFixedBytes(stateID)
		return NFS4OK

	case OpRead:
		_, _ = reader.ReadFixedBytes(16) // stateid
		offset, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		count, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}

		_, abs, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		f, err := os.Open(abs)
		if err != nil {
			out.WriteUint32(NFS4ERRNOENT)
			return NFS4ERRNOENT
		}
		defer f.Close()

		data := make([]byte, count)
		n, readErr := f.ReadAt(data, int64(offset))
		eof := false
		if readErr == io.EOF || n < int(count) {
			eof = true
		}

		out.WriteUint32(NFS4OK)
		if eof {
			out.WriteUint32(1)
		} else {
			out.WriteUint32(0)
		}
		out.WriteBytes(data[:n])
		return NFS4OK

	case OpWrite:
		_, _ = reader.ReadFixedBytes(16) // stateid
		offset, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		_, _ = reader.ReadUint32() // stable_how
		data, err := reader.ReadBytes()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}

		_, abs, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			out.WriteUint32(NFS4ERRACCESS)
			return NFS4ERRACCESS
		}
		defer f.Close()

		n, err := f.WriteAt(data, int64(offset))
		if err != nil {
			out.WriteUint32(NFS4ERRIO)
			return NFS4ERRIO
		}

		out.WriteUint32(NFS4OK)
		out.WriteUint32(uint32(n))
		out.WriteUint32(2) // committed = FILE_SYNC4 (2)
		var verf [8]byte
		binary.BigEndian.PutUint64(verf[:], uint64(h.bootTime.Unix()))
		out.WriteFixedBytes(verf[:])
		return NFS4OK

	case OpRemove:
		target, err := reader.ReadString()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		rel, _, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		targetRel := filepath.Clean(filepath.Join(rel, target))
		targetAbs := filepath.Join(h.rootDir, targetRel)

		if err := os.Remove(targetAbs); err != nil {
			if os.IsNotExist(err) {
				out.WriteUint32(NFS4ERRNOENT)
				return NFS4ERRNOENT
			}
			out.WriteUint32(NFS4ERRACCESS)
			return NFS4ERRACCESS
		}

		out.WriteUint32(NFS4OK)
		// cinfo: change_info4
		out.WriteUint32(1) // atomic = true
		out.WriteUint64(uint64(time.Now().UnixNano()))
		out.WriteUint64(uint64(time.Now().UnixNano()))
		return NFS4OK

	case OpSetattr:
		_, _ = reader.ReadFixedBytes(16) // stateid
		reqBm, err := reader.ReadBitmap()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		_, _ = reader.ReadBytes() // attr_vals
		out.WriteUint32(NFS4OK)
		out.WriteBitmap(reqBm)
		return NFS4OK

	case OpCreate:
		objType, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		var linkData string
		if objType == NF4LNK {
			linkData, err = reader.ReadString()
			if err != nil {
				out.WriteUint32(NFS4ERRINVAL)
				return NFS4ERRINVAL
			}
		} else if objType == NF4BLK || objType == NF4CHR {
			_, _ = reader.ReadUint32() // major
			_, _ = reader.ReadUint32() // minor
		}

		objName, err := reader.ReadString()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		_ = skipBitmapAttrs(reader)

		rel, _, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		targetRel := filepath.Clean(filepath.Join(rel, objName))
		targetAbs := filepath.Join(h.rootDir, targetRel)

		switch objType {
		case NF4DIR:
			if err := os.Mkdir(targetAbs, 0755); err != nil {
				if os.IsExist(err) {
					out.WriteUint32(NFS4ERREXIST)
					return NFS4ERREXIST
				}
				out.WriteUint32(NFS4ERRACCESS)
				return NFS4ERRACCESS
			}
		case NF4LNK:
			if err := os.Symlink(linkData, targetAbs); err != nil {
				if os.IsExist(err) {
					out.WriteUint32(NFS4ERREXIST)
					return NFS4ERREXIST
				}
				out.WriteUint32(NFS4ERRACCESS)
				return NFS4ERRACCESS
			}
		default:
			f, err := os.Create(targetAbs)
			if err != nil {
				out.WriteUint32(NFS4ERRACCESS)
				return NFS4ERRACCESS
			}
			_ = f.Close()
		}

		// RFC 7530 16.4.4: On success, the current filehandle is set to the new object
		state.CFH = h.hm.GetOrCreateFH(targetRel)

		out.WriteUint32(NFS4OK)
		// cinfo: change_info4
		out.WriteUint32(1) // atomic = true
		out.WriteUint64(uint64(time.Now().UnixNano()))
		out.WriteUint64(uint64(time.Now().UnixNano()))
		// attrset: bitmap4
		out.WriteBitmap([]uint32{0, 0})
		return NFS4OK

	case OpReadlink:
		_, abs, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		target, err := os.Readlink(abs)
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		out.WriteUint32(NFS4OK)
		out.WriteString(target)
		return NFS4OK

	case OpRename:
		oldName, err := reader.ReadString()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		newName, err := reader.ReadString()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}

		srcRel, _, ok1 := h.hm.ResolvePath(state.SFH)
		dstRel, _, ok2 := h.hm.ResolvePath(state.CFH)
		if !ok1 || !ok2 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		oldRel := filepath.Clean(filepath.Join(srcRel, oldName))
		newRel := filepath.Clean(filepath.Join(dstRel, newName))
		oldAbs := filepath.Join(h.rootDir, oldRel)
		newAbs := filepath.Join(h.rootDir, newRel)

		if err := os.Rename(oldAbs, newAbs); err != nil {
			if os.IsNotExist(err) {
				out.WriteUint32(NFS4ERRNOENT)
				return NFS4ERRNOENT
			}
			out.WriteUint32(NFS4ERRACCESS)
			return NFS4ERRACCESS
		}

		h.hm.Rename(oldRel, newRel)

		out.WriteUint32(NFS4OK)
		// source_cinfo
		out.WriteUint32(1)
		out.WriteUint64(uint64(time.Now().UnixNano()))
		out.WriteUint64(uint64(time.Now().UnixNano()))
		// target_cinfo
		out.WriteUint32(1)
		out.WriteUint64(uint64(time.Now().UnixNano()))
		out.WriteUint64(uint64(time.Now().UnixNano()))
		return NFS4OK

	case OpCommit:
		_, _ = reader.ReadUint64() // offset
		_, _ = reader.ReadUint32() // count
		out.WriteUint32(NFS4OK)
		var verf [8]byte
		binary.BigEndian.PutUint64(verf[:], uint64(h.bootTime.Unix()))
		out.WriteFixedBytes(verf[:])
		return NFS4OK

	case OpLink:
		newName, err := reader.ReadString()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		_, srcAbs, ok1 := h.hm.ResolvePath(state.SFH)
		dstRel, dstAbs, ok2 := h.hm.ResolvePath(state.CFH)
		if !ok1 || !ok2 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		targetAbs := filepath.Join(dstAbs, newName)
		if err := os.Link(srcAbs, targetAbs); err != nil {
			if os.IsExist(err) {
				out.WriteUint32(NFS4ERREXIST)
				return NFS4ERREXIST
			}
			out.WriteUint32(NFS4ERRACCESS)
			return NFS4ERRACCESS
		}

		targetRel := filepath.Clean(filepath.Join(dstRel, newName))
		h.hm.GetOrCreateFH(targetRel)

		out.WriteUint32(NFS4OK)
		out.WriteUint32(1) // atomic = true
		out.WriteUint64(uint64(time.Now().UnixNano()))
		out.WriteUint64(uint64(time.Now().UnixNano()))
		return NFS4OK

	case OpLock:
		lockType, _ := reader.ReadUint32()
		_, _ = reader.ReadUint32() // reclaim
		offset, _ := reader.ReadUint64()
		length, _ := reader.ReadUint64()
		lockerType, _ := reader.ReadUint32()
		var clientID uint64
		var owner string
		var existingStateID [16]byte
		isNewOwner := (lockerType == 1)
		if isNewOwner {
			_, _ = reader.ReadUint32()       // open_seqid
			_, _ = reader.ReadFixedBytes(16) // open_stateid
			_, _ = reader.ReadUint32()       // lock_seqid
			clientID, _ = reader.ReadUint64()
			owner, _ = reader.ReadString()
		} else {
			rawSID, _ := reader.ReadFixedBytes(16) // lock_stateid
			copy(existingStateID[:], rawSID)
			_, _ = reader.ReadUint32()       // lock_seqid
		}

		rel, _, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		sid, conflict := h.lm.AcquireLock(rel, lockType, offset, length, isNewOwner, clientID, owner, existingStateID)
		if conflict != nil {
			out.WriteUint32(NFS4ERRDENIED)
			out.WriteUint64(conflict.Offset)
			out.WriteUint64(conflict.Length)
			out.WriteUint32(conflict.Type)
			out.WriteUint64(conflict.ClientID)
			out.WriteString(conflict.Owner)
			return NFS4ERRDENIED
		}

		out.WriteUint32(NFS4OK)
		out.WriteFixedBytes(sid[:])
		return NFS4OK

	case OpLockt:
		lockType, _ := reader.ReadUint32()
		offset, _ := reader.ReadUint64()
		length, _ := reader.ReadUint64()
		clientID, _ := reader.ReadUint64()
		owner, _ := reader.ReadString()

		rel, _, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		conflict := h.lm.CheckConflict(rel, lockType, offset, length, clientID, owner)
		if conflict != nil {
			out.WriteUint32(NFS4ERRDENIED)
			out.WriteUint64(conflict.Offset)
			out.WriteUint64(conflict.Length)
			out.WriteUint32(conflict.Type)
			out.WriteUint64(conflict.ClientID)
			out.WriteString(conflict.Owner)
			return NFS4ERRDENIED
		}

		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpLocku:
		_, _ = reader.ReadUint32() // locktype
		_, _ = reader.ReadUint32() // seqid
		lockStateID, _ := reader.ReadFixedBytes(16)
		offset, _ := reader.ReadUint64()
		length, _ := reader.ReadUint64()

		rel, _, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		var sid [16]byte
		copy(sid[:], lockStateID)
		newSid, _ := h.lm.Unlock(rel, offset, length, sid)

		out.WriteUint32(NFS4OK)
		out.WriteFixedBytes(newSid[:])
		return NFS4OK

	case OpReleaseLockowner:
		clientID, _ := reader.ReadUint64()
		owner, _ := reader.ReadString()
		h.lm.ReleaseLockOwner(clientID, owner)
		out.WriteUint32(NFS4OK)
		return NFS4OK

	// -------------------------------------------------------------
	// NFSv4.1 & NFSv4.2 Session and Modern Operations
	// -------------------------------------------------------------
	case OpExchangeId:
		var verifier [8]byte
		v, err := reader.ReadFixedBytes(8)
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		copy(verifier[:], v)
		ownerID, err := reader.ReadString()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		flags, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		_ = flags

		// state_protect4_a
		spaHow, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		if spaHow == 1 { // SP4_MACH_CRED
			_, _ = reader.ReadBitmap() // spo_must_enforce
			_, _ = reader.ReadBitmap() // spo_must_allow
		}

		// client_impl_id<1>
		implCount, err := reader.ReadUint32()
		if err == nil && implCount > 0 {
			_, _ = reader.ReadString() // nii_domain
			_, _ = reader.ReadString() // nii_name
			_, _ = reader.ReadUint64() // seconds
			_, _ = reader.ReadUint32() // nseconds
		}

		h.clientsMu.Lock()
		cid := h.nextCID
		h.nextCID++
		client := &ClientState{
			ClientID:  cid,
			Verifier:  verifier,
			ClientStr: ownerID,
		}
		h.clients[cid] = client
		h.clientsMu.Unlock()

		out.WriteUint32(NFS4OK)
		out.WriteUint64(cid)                    // eir_clientid
		out.WriteUint32(1)                      // eir_sequenceid
		out.WriteUint32(Exchgid4FlagUseNonPnfs) // eir_flags (0x00010000)
		out.WriteUint32(0)                      // eir_state_protect: spr_how = SP4_NONE
		// eir_server_owner
		out.WriteUint64(1)                       // so_minor_id
		out.WriteBytes([]byte("blackhole-nfs"))   // so_major_id
		out.WriteBytes([]byte("blackhole-scope")) // eir_server_scope
		out.WriteUint32(0)                       // eir_server_impl_id count = 0
		return NFS4OK

	case OpCreateSession:
		cid, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		seq, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		flags, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}

		// fore channel attrs
		_, _ = reader.ReadUint32() // headerpadsize
		foreMaxReq, _ := reader.ReadUint32()
		foreMaxResp, _ := reader.ReadUint32()
		_, _ = reader.ReadUint32() // maxresponsesize_cached
		_, _ = reader.ReadUint32() // maxops
		foreMaxRequests, _ := reader.ReadUint32()
		rdmaIrdCount, _ := reader.ReadUint32()
		if rdmaIrdCount > 0 {
			_, _ = reader.ReadUint32()
		}

		// back channel attrs
		_, _ = reader.ReadUint32() // headerpadsize
		_, _ = reader.ReadUint32() // maxreq
		_, _ = reader.ReadUint32() // maxresp
		_, _ = reader.ReadUint32() // maxresp_cached
		_, _ = reader.ReadUint32() // maxops
		_, _ = reader.ReadUint32() // maxrequests
		rdmaIrdCount2, _ := reader.ReadUint32()
		if rdmaIrdCount2 > 0 {
			_, _ = reader.ReadUint32()
		}

		// callback program
		_, _ = reader.ReadUint32()

		// sec parms
		secCount, _ := reader.ReadUint32()
		for j := uint32(0); j < secCount; j++ {
			flavor, _ := reader.ReadUint32()
			if flavor == AuthFlavorSys {
				_, _ = reader.ReadUint32() // stamp
				_, _ = reader.ReadString() // machinename
				_, _ = reader.ReadUint32() // uid
				_, _ = reader.ReadUint32() // gid
				gCount, _ := reader.ReadUint32()
				for k := uint32(0); k < gCount; k++ {
					_, _ = reader.ReadUint32()
				}
			}
		}

		if foreMaxReq == 0 || foreMaxReq > 1048576 {
			foreMaxReq = 1048576
		}
		if foreMaxResp == 0 || foreMaxResp > 1048576 {
			foreMaxResp = 1048576
		}
		if foreMaxRequests == 0 || foreMaxRequests > 64 {
			foreMaxRequests = 64
		}

		session := h.sm.CreateSession(cid, flags, foreMaxRequests)

		out.WriteUint32(NFS4OK)
		out.WriteFixedBytes(session.SessionID[:]) // csr_sessionid
		out.WriteUint32(seq)                     // csr_sequence
		out.WriteUint32(0)                       // csr_flags
		// csr_fore_chan_attrs
		out.WriteUint32(0)               // ca_headerpadsize
		out.WriteUint32(foreMaxReq)      // ca_maxrequestsize
		out.WriteUint32(foreMaxResp)     // ca_maxresponsesize
		out.WriteUint32(4096)            // ca_maxresponsesize_cached
		out.WriteUint32(64)              // ca_maxoperations
		out.WriteUint32(foreMaxRequests) // ca_maxrequests
		out.WriteUint32(0)               // ca_rdma_ird count = 0
		// csr_back_chan_attrs
		out.WriteUint32(0)    // ca_headerpadsize
		out.WriteUint32(4096) // ca_maxrequestsize
		out.WriteUint32(4096) // ca_maxresponsesize
		out.WriteUint32(0)    // ca_maxresponsesize_cached
		out.WriteUint32(16)   // ca_maxoperations
		out.WriteUint32(16)   // ca_maxrequests
		out.WriteUint32(0)    // ca_rdma_ird count = 0
		return NFS4OK

	case OpSequence:
		sessBytes, err := reader.ReadFixedBytes(16)
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		var sessID [16]byte
		copy(sessID[:], sessBytes)

		seqID, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		slotID, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		highestSlotID, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		_, _ = reader.ReadUint32() // cachethis

		status, targetHighest := h.sm.ProcessSequence(sessID, seqID, slotID, highestSlotID)
		if status != NFS4OK {
			out.WriteUint32(status)
			return status
		}

		out.WriteUint32(NFS4OK)
		out.WriteFixedBytes(sessID[:])
		out.WriteUint32(seqID)
		out.WriteUint32(slotID)
		out.WriteUint32(targetHighest) // highest_slotid
		out.WriteUint32(targetHighest) // target_highest_slotid
		out.WriteUint32(0)             // status_flags
		return NFS4OK

	case OpDestroySession:
		sessBytes, err := reader.ReadFixedBytes(16)
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		var sessID [16]byte
		copy(sessID[:], sessBytes)
		h.sm.DestroySession(sessID)
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpDestroyClientid:
		cid, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		h.clientsMu.Lock()
		delete(h.clients, cid)
		h.clientsMu.Unlock()
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpReclaimComplete:
		_, _ = reader.ReadUint32() // rca_one_fs
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpSecinfoNoName:
		_, _ = reader.ReadUint32() // secinfo_style
		out.WriteUint32(NFS4OK)
		out.WriteUint32(1)             // count = 1
		out.WriteUint32(AuthFlavorSys) // flavor = AUTH_SYS
		return NFS4OK

	case OpTestStateid:
		count, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		for j := uint32(0); j < count; j++ {
			_, _ = reader.ReadFixedBytes(16)
		}
		out.WriteUint32(NFS4OK)
		out.WriteUint32(count)
		for j := uint32(0); j < count; j++ {
			out.WriteUint32(NFS4OK)
		}
		return NFS4OK

	case OpFreeStateid:
		_, _ = reader.ReadFixedBytes(16)
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpCopy:
		_, _ = reader.ReadFixedBytes(16) // ca_src_stateid
		_, _ = reader.ReadFixedBytes(16) // ca_dst_stateid
		srcOffset, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		dstOffset, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		count, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		_, _ = reader.ReadUint32() // ca_consecutive
		_, _ = reader.ReadUint32() // ca_synchronous
		netlocCount, _ := reader.ReadUint32()
		for j := uint32(0); j < netlocCount; j++ {
			_, _ = reader.ReadString()
		}

		if len(state.SFH) == 0 || len(state.CFH) == 0 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		_, srcAbs, ok1 := h.hm.ResolvePath(state.SFH)
		_, dstAbs, ok2 := h.hm.ResolvePath(state.CFH)
		if !ok1 || !ok2 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		srcFile, err := os.Open(srcAbs)
		if err != nil {
			if os.IsNotExist(err) {
				out.WriteUint32(NFS4ERRNOENT)
				return NFS4ERRNOENT
			}
			out.WriteUint32(NFS4ERRACCESS)
			return NFS4ERRACCESS
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(dstAbs, os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			out.WriteUint32(NFS4ERRACCESS)
			return NFS4ERRACCESS
		}
		defer dstFile.Close()

		if _, err := srcFile.Seek(int64(srcOffset), io.SeekStart); err != nil {
			out.WriteUint32(NFS4ERRIO)
			return NFS4ERRIO
		}
		if _, err := dstFile.Seek(int64(dstOffset), io.SeekStart); err != nil {
			out.WriteUint32(NFS4ERRIO)
			return NFS4ERRIO
		}

		copied, err := io.CopyN(dstFile, srcFile, int64(count))
		if err != nil && err != io.EOF {
			out.WriteUint32(NFS4ERRIO)
			return NFS4ERRIO
		}
		_ = dstFile.Sync()

		bootVerifier := make([]byte, 8)
		binary.BigEndian.PutUint64(bootVerifier, uint64(h.bootTime.Unix()))

		out.WriteUint32(NFS4OK)
		// write_response4
		out.WriteUint32(0)                // wr_callback_id count = 0
		out.WriteUint64(uint64(copied))   // wr_count
		out.WriteUint32(2)                // wr_committed = FILE_SYNC4 (2)
		out.WriteFixedBytes(bootVerifier) // wr_writeverf (8B)
		// copy_requirements4
		out.WriteUint32(1) // cr_consecutive = true
		out.WriteUint32(1) // cr_synchronous = true
		return NFS4OK

	case OpAllocate:
		_, _ = reader.ReadFixedBytes(16) // aa_stateid
		offset, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		length, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}

		if len(state.CFH) == 0 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		_, abs, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		f, err := os.OpenFile(abs, os.O_WRONLY, 0644)
		if err != nil {
			out.WriteUint32(NFS4ERRIO)
			return NFS4ERRIO
		}
		defer f.Close()

		targetSize := int64(offset + length)
		fi, err := f.Stat()
		if err == nil && targetSize > fi.Size() {
			_ = f.Truncate(targetSize)
		}

		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpDeallocate:
		_, _ = reader.ReadFixedBytes(16) // da_stateid
		_, _ = reader.ReadUint64()        // da_offset
		_, _ = reader.ReadUint64()        // da_length
		if len(state.CFH) == 0 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		out.WriteUint32(NFS4OK)
		return NFS4OK

	case OpSeek:
		_, _ = reader.ReadFixedBytes(16) // sa_stateid
		offset, err := reader.ReadUint64()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}
		what, err := reader.ReadUint32()
		if err != nil {
			out.WriteUint32(NFS4ERRINVAL)
			return NFS4ERRINVAL
		}

		if len(state.CFH) == 0 {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}
		_, abs, ok := h.hm.ResolvePath(state.CFH)
		if !ok {
			out.WriteUint32(NFS4ERRBADHANDLE)
			return NFS4ERRBADHANDLE
		}

		fi, err := os.Stat(abs)
		if err != nil {
			out.WriteUint32(NFS4ERRNOENT)
			return NFS4ERRNOENT
		}

		size := uint64(fi.Size())
		var eof uint32
		var resOff uint64
		if what == 0 { // DATA
			if offset < size {
				eof = 0
				resOff = offset
			} else {
				eof = 1
				resOff = size
			}
		} else { // HOLE
			eof = 1
			resOff = size
		}

		out.WriteUint32(NFS4OK)
		out.WriteUint32(eof)
		out.WriteUint64(resOff)
		return NFS4OK

	default:
		// Unsupported operations
		out.WriteUint32(NFS4ERRNOTSUPP)
		return NFS4ERRNOTSUPP
	}
}

// encodeAttributes builds fattr4 according to requested attribute bitmap.
func (h *NFSHandler) encodeAttributes(w *XDRWriter, reqBm []uint32, fi os.FileInfo, relPath string, fh []byte) {
	reqW0 := uint32(0)
	reqW1 := uint32(0)
	if len(reqBm) > 0 {
		reqW0 = reqBm[0]
	}
	if len(reqBm) > 1 {
		reqW1 = reqBm[1]
	}

	supportedW0 := Fattr4SupportedAttrs | Fattr4Type | Fattr4FhExpireType | Fattr4Change | Fattr4Size |
		Fattr4LinkSupport | Fattr4SymlinkSupport | Fattr4NamedAttr | Fattr4Fsid | Fattr4UniqueHandles |
		Fattr4LeaseTime | Fattr4RdattrError | Fattr4Filehandle | Fattr4Fileid | Fattr4FilesAvail |
		Fattr4FilesFree | Fattr4FilesTotal | Fattr4Maxfilesize | Fattr4Maxname | Fattr4Maxread | Fattr4Maxwrite

	supportedW1 := Fattr4Mode | Fattr4NoTrunc | Fattr4Nummultilinks | Fattr4Owner | Fattr4OwnerGroup |
		Fattr4SpaceAvail | Fattr4SpaceFree | Fattr4SpaceTotal | Fattr4SpaceUsed | Fattr4TimeAccess |
		Fattr4TimeMetadata | Fattr4TimeModify | Fattr4MountedOnFileid

	respW0 := reqW0 & supportedW0
	respW1 := reqW1 & supportedW1

	var ino uint64 = 1
	var size uint64 = 4096
	var modTime = time.Now()
	var mode uint32 = 0755
	fType := NF4DIR

	if fi != nil {
		if fi.IsDir() {
			fType = NF4DIR
		} else if (fi.Mode() & os.ModeSymlink) != 0 {
			fType = NF4LNK
		} else {
			fType = NF4REG
		}
		size = uint64(fi.Size())
		modTime = fi.ModTime()
		mode = uint32(fi.Mode().Perm())
		if stat, ok := fi.Sys().(*syscall.Stat_t); ok {
			ino = stat.Ino
		}
	}

	// First, serialize attribute values into temporary buffer in canonical order
	vals := NewXDRWriter()

	// Word 0 attributes
	if (respW0 & Fattr4SupportedAttrs) != 0 {
		vals.WriteBitmap([]uint32{supportedW0, supportedW1})
	}
	if (respW0 & Fattr4Type) != 0 {
		vals.WriteUint32(fType)
	}
	if (respW0 & Fattr4FhExpireType) != 0 {
		vals.WriteUint32(0) // FH4_PERSISTENT
	}
	if (respW0 & Fattr4Change) != 0 {
		vals.WriteUint64(uint64(modTime.UnixNano()))
	}
	if (respW0 & Fattr4Size) != 0 {
		vals.WriteUint64(size)
	}
	if (respW0 & Fattr4LinkSupport) != 0 {
		vals.WriteUint32(1)
	}
	if (respW0 & Fattr4SymlinkSupport) != 0 {
		vals.WriteUint32(1)
	}
	if (respW0 & Fattr4NamedAttr) != 0 {
		vals.WriteUint32(0)
	}
	if (respW0 & Fattr4Fsid) != 0 {
		vals.WriteUint64(1) // Major
		vals.WriteUint64(1) // Minor
	}
	if (respW0 & Fattr4UniqueHandles) != 0 {
		vals.WriteUint32(1)
	}
	if (respW0 & Fattr4LeaseTime) != 0 {
		vals.WriteUint32(60) // 60s lease
	}
	if (respW0 & Fattr4RdattrError) != 0 {
		vals.WriteUint32(NFS4OK)
	}
	if (respW0 & Fattr4Filehandle) != 0 {
		vals.WriteBytes(fh)
	}
	if (respW0 & Fattr4Fileid) != 0 {
		vals.WriteUint64(ino)
	}
	if (respW0 & Fattr4FilesAvail) != 0 {
		vals.WriteUint64(10000000)
	}
	if (respW0 & Fattr4FilesFree) != 0 {
		vals.WriteUint64(10000000)
	}
	if (respW0 & Fattr4FilesTotal) != 0 {
		vals.WriteUint64(20000000)
	}
	if (respW0 & Fattr4Maxfilesize) != 0 {
		vals.WriteUint64(0x7fffffffffffffff)
	}
	if (respW0 & Fattr4Maxname) != 0 {
		vals.WriteUint32(255)
	}
	if (respW0 & Fattr4Maxread) != 0 {
		vals.WriteUint64(1048576) // 1MB
	}
	if (respW0 & Fattr4Maxwrite) != 0 {
		vals.WriteUint64(1048576) // 1MB
	}

	// Word 1 attributes
	if (respW1 & Fattr4Mode) != 0 {
		vals.WriteUint32(mode)
	}
	if (respW1 & Fattr4NoTrunc) != 0 {
		vals.WriteUint32(1)
	}
	if (respW1 & Fattr4Nummultilinks) != 0 {
		vals.WriteUint32(1)
	}
	if (respW1 & Fattr4Owner) != 0 {
		vals.WriteString("0")
	}
	if (respW1 & Fattr4OwnerGroup) != 0 {
		vals.WriteString("0")
	}
	if (respW1 & Fattr4SpaceAvail) != 0 {
		vals.WriteUint64(100 * 1024 * 1024 * 1024)
	}
	if (respW1 & Fattr4SpaceFree) != 0 {
		vals.WriteUint64(100 * 1024 * 1024 * 1024)
	}
	if (respW1 & Fattr4SpaceTotal) != 0 {
		vals.WriteUint64(500 * 1024 * 1024 * 1024)
	}
	if (respW1 & Fattr4SpaceUsed) != 0 {
		vals.WriteUint64(400 * 1024 * 1024 * 1024)
	}
	if (respW1 & Fattr4TimeAccess) != 0 {
		vals.WriteUint64(uint64(modTime.Unix()))
		vals.WriteUint32(uint32(modTime.Nanosecond()))
	}
	if (respW1 & Fattr4TimeMetadata) != 0 {
		vals.WriteUint64(uint64(modTime.Unix()))
		vals.WriteUint32(uint32(modTime.Nanosecond()))
	}
	if (respW1 & Fattr4TimeModify) != 0 {
		vals.WriteUint64(uint64(modTime.Unix()))
		vals.WriteUint32(uint32(modTime.Nanosecond()))
	}
	if (respW1 & Fattr4MountedOnFileid) != 0 {
		vals.WriteUint64(ino)
	}

	// Write fattr4 header: bitmap4, then opaque attr_vals
	w.WriteBitmap([]uint32{respW0, respW1})
	w.WriteBytes(vals.Bytes())
}

func skipBitmapAttrs(reader *XDRReader) error {
	bm, err := reader.ReadBitmap()
	if err != nil {
		return err
	}
	_ = bm
	_, err = reader.ReadBytes() // opaque vals
	return err
}
