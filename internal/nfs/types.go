package nfs

// ONC RPC Program & Procedure constants
const (
	RPCProgramNFS uint32 = 100003
	RPCVersion4   uint32 = 4

	RPCProcNull     uint32 = 0
	RPCProcCompound uint32 = 1

	// RPC Message Types
	RPCMsgCall  uint32 = 0
	RPCMsgReply uint32 = 1

	// Reply Status
	RPCMsgAccepted uint32 = 0
	RPCMsgDenied   uint32 = 1

	// Accept Status
	RPCAcceptSuccess uint32 = 0

	// Auth Flavors
	AuthFlavorNone uint32 = 0
	AuthFlavorSys  uint32 = 1
)

// NFSv4 Status Codes (nfsstat4) - RFC 7530
const (
	NFS4OK             uint32 = 0
	NFS4ERRPERM        uint32 = 1
	NFS4ERRNOENT       uint32 = 2
	NFS4ERRIO          uint32 = 5
	NFS4ERRACCESS      uint32 = 13
	NFS4ERREXIST       uint32 = 17
	NFS4ERRNOTDIR      uint32 = 20
	NFS4ERRISDIR       uint32 = 21
	NFS4ERRINVAL       uint32 = 22
	NFS4ERRFBIG        uint32 = 27
	NFS4ERRNOSPC       uint32 = 28
	NFS4ERRROFS        uint32 = 30
	NFS4ERRNAMETOOLONG uint32 = 63
	NFS4ERRNOTEMPTY    uint32 = 66
	NFS4ERRSTALE       uint32 = 70
	NFS4ERRBADHANDLE   uint32 = 10001
	NFS4ERRBADCOOKIE   uint32 = 10003
	NFS4ERRNOTSUPP     uint32 = 10004
	NFS4ERRTOOSMALL    uint32 = 10005
	NFS4ERRSERVERFAULT uint32 = 10006
	NFS4ERRBADTYPE     uint32 = 10007
	NFS4ERRDELAY       uint32 = 10008
	NFS4ERRSAME        uint32 = 10009
	NFS4ERRDENIED      uint32 = 10010
	NFS4ERREXPIRED     uint32 = 10011
	NFS4ERRLOCKED      uint32 = 10012
	NFS4ERRGRACE       uint32 = 10013
	NFS4ERRFHEXPIRED   uint32 = 10014
	NFS4ERRSHAREDENIED uint32 = 10015
	NFS4ERRWRONGFH     uint32 = 10016
	NFS4ERRCLIDINUSE   uint32 = 10017
	NFS4ERRMINORVERSMISMATCH uint32 = 10021
	NFS4ERRBADSEQID    uint32 = 10026
	NFS4ERRNOTSAME     uint32 = 10027
	NFS4ERRBADSTATEID  uint32 = 10036
	NFS4ERRBADSESSION  uint32 = 10052
	NFS4ERRBADSLOT     uint32 = 10053
	NFS4ERRSEQMISORDERED uint32 = 10063
	NFS4ERROFFLOADNOTSUPP uint32 = 10091
)

// NFSv4 Operation Codes (nfs_opnum4) - RFC 7530, RFC 5661, RFC 7862
const (
	OpAccess            uint32 = 3
	OpClose             uint32 = 4
	OpCommit            uint32 = 5
	OpCreate            uint32 = 6
	OpDelegpurge        uint32 = 7
	OpDelegreturn       uint32 = 8
	OpGetattr           uint32 = 9
	OpGetfh             uint32 = 10
	OpLink              uint32 = 11
	OpLock              uint32 = 12
	OpLockt             uint32 = 13
	OpLocku             uint32 = 14
	OpLookup            uint32 = 15
	OpLookupp           uint32 = 16
	OpNverify           uint32 = 17
	OpOpen              uint32 = 18
	OpOpenAttr          uint32 = 19
	OpOpenConfirm       uint32 = 20
	OpOpenDowngrade     uint32 = 21
	OpPutfh             uint32 = 22
	OpPutpubfh          uint32 = 23
	OpPutrootfh         uint32 = 24
	OpRead              uint32 = 25
	OpReaddir           uint32 = 26
	OpReadlink          uint32 = 27
	OpRemove            uint32 = 28
	OpRename            uint32 = 29
	OpRenew             uint32 = 30
	OpRestorefh         uint32 = 31
	OpSavefh            uint32 = 32
	OpSecinfo           uint32 = 33
	OpSetattr           uint32 = 34
	OpSetclientid       uint32 = 35
	OpSetclientidConf   uint32 = 36
	OpVerify            uint32 = 37
	OpWrite             uint32 = 38
	OpReleaseLockowner  uint32 = 39

	// NFSv4.1 Operations (RFC 5661)
	OpExchangeId        uint32 = 42
	OpCreateSession     uint32 = 43
	OpDestroySession    uint32 = 44
	OpFreeStateid       uint32 = 45
	OpGetDirDelegation  uint32 = 46
	OpGetdeviceinfo     uint32 = 47
	OpGetdevicelist     uint32 = 48
	OpLayoutcommit      uint32 = 49
	OpLayoutget         uint32 = 50
	OpLayoutreturn      uint32 = 51
	OpSecinfoNoName     uint32 = 52
	OpSequence          uint32 = 53
	OpSetSsv            uint32 = 54
	OpTestStateid       uint32 = 55
	OpWantDelegation    uint32 = 56
	OpDestroyClientid   uint32 = 57
	OpReclaimComplete   uint32 = 58

	// NFSv4.2 Operations (RFC 7862)
	OpAllocate          uint32 = 59
	OpCopy              uint32 = 60
	OpCopyNotify        uint32 = 61
	OpDeallocate        uint32 = 62
	OpIoAdvise          uint32 = 63
	OpLayouterror       uint32 = 64
	OpLayoutstats       uint32 = 65
	OpOffloadCancel     uint32 = 66
	OpOffloadStatus     uint32 = 67
	OpReadPlus          uint32 = 68
	OpSeek              uint32 = 69
	OpWriteSame         uint32 = 70
	OpClone             uint32 = 71

	OpIllegal           uint32 = 10044
)

// NFSv4.1 Flags
const (
	Exchgid4FlagUseNonPnfs uint32 = 0x00010000
	Exchgid4FlagConfirmedR uint32 = 0x80000000
)

// NFSv4 File Types (nfs_ftype4)
const (
	NF4REG  uint32 = 1 // Regular File
	NF4DIR  uint32 = 2 // Directory
	NF4BLK  uint32 = 3 // Block Device
	NF4CHR  uint32 = 4 // Character Device
	NF4LNK  uint32 = 5 // Symbolic Link
	NF4SOCK uint32 = 6 // Socket
	NF4FIFO uint32 = 7 // Named Pipe
)

// Access mask bits (ACCESS4_*)
const (
	Access4Read    uint32 = 0x00000001
	Access4Lookup  uint32 = 0x00000002
	Access4Modify  uint32 = 0x00000004
	Access4Extend  uint32 = 0x00000008
	Access4Delete  uint32 = 0x00000010
	Access4Execute uint32 = 0x00000020
)

// Standard Attribute Bitmask Positions (RFC 7530 Section 5.8)
// Word 0 attributes (bits 0..31)
const (
	Fattr4SupportedAttrs uint32 = 1 << 0
	Fattr4Type           uint32 = 1 << 1
	Fattr4FhExpireType   uint32 = 1 << 2
	Fattr4Change         uint32 = 1 << 3
	Fattr4Size           uint32 = 1 << 4
	Fattr4LinkSupport    uint32 = 1 << 5
	Fattr4SymlinkSupport uint32 = 1 << 6
	Fattr4NamedAttr      uint32 = 1 << 7
	Fattr4Fsid           uint32 = 1 << 8
	Fattr4UniqueHandles  uint32 = 1 << 9
	Fattr4LeaseTime      uint32 = 1 << 10
	Fattr4RdattrError    uint32 = 1 << 11
	Fattr4Acl            uint32 = 1 << 12
	Fattr4Aclsupport     uint32 = 1 << 13
	Fattr4Archive        uint32 = 1 << 14
	Fattr4Cansettime     uint32 = 1 << 15
	Fattr4CaseInsensitive uint32 = 1 << 16
	Fattr4CasePreserving uint32 = 1 << 17
	Fattr4ChownRestricted uint32 = 1 << 18
	Fattr4Filehandle     uint32 = 1 << 19
	Fattr4Fileid         uint32 = 1 << 20
	Fattr4FilesAvail     uint32 = 1 << 21
	Fattr4FilesFree      uint32 = 1 << 22
	Fattr4FilesTotal     uint32 = 1 << 23
	Fattr4FsLocations    uint32 = 1 << 24
	Fattr4Hidden         uint32 = 1 << 25
	Fattr4Homogeneous    uint32 = 1 << 26
	Fattr4Maxfilesize    uint32 = 1 << 27
	Fattr4Maxlink        uint32 = 1 << 28
	Fattr4Maxname        uint32 = 1 << 29
	Fattr4Maxread        uint32 = 1 << 30
	Fattr4Maxwrite       uint32 = 1 << 31
)

// Word 1 attributes (bits 32..63 -> represented in word 1 as bit 0..31)
const (
	Fattr4Mode           uint32 = 1 << (33 - 32) // Bit 33 (word 1 bit 1)
	Fattr4NoTrunc        uint32 = 1 << (34 - 32) // Bit 34 (word 1 bit 2)
	Fattr4Nummultilinks  uint32 = 1 << (35 - 32) // Bit 35 (word 1 bit 3)
	Fattr4Owner          uint32 = 1 << (36 - 32) // Bit 36 (word 1 bit 4)
	Fattr4OwnerGroup     uint32 = 1 << (37 - 32) // Bit 37 (word 1 bit 5)
	Fattr4QuotaAvailHard uint32 = 1 << (38 - 32)
	Fattr4QuotaAvailSoft uint32 = 1 << (39 - 32)
	Fattr4QuotaUsed      uint32 = 1 << (40 - 32)
	Fattr4Rawdev         uint32 = 1 << (41 - 32)
	Fattr4SpaceAvail     uint32 = 1 << (42 - 32) // Bit 42 (word 1 bit 10)
	Fattr4SpaceFree      uint32 = 1 << (43 - 32) // Bit 43 (word 1 bit 11)
	Fattr4SpaceTotal     uint32 = 1 << (44 - 32) // Bit 44 (word 1 bit 12)
	Fattr4SpaceUsed      uint32 = 1 << (45 - 32) // Bit 45 (word 1 bit 13)
	Fattr4System         uint32 = 1 << (46 - 32)
	Fattr4TimeAccess     uint32 = 1 << (47 - 32) // Bit 47 (word 1 bit 15)
	Fattr4TimeAccessSet  uint32 = 1 << (48 - 32)
	Fattr4TimeBackup     uint32 = 1 << (49 - 32)
	Fattr4TimeCreate     uint32 = 1 << (50 - 32)
	Fattr4TimeDirModify  uint32 = 1 << (51 - 32)
	Fattr4TimeMetadata   uint32 = 1 << (52 - 32) // Bit 52 (word 1 bit 20)
	Fattr4TimeModify     uint32 = 1 << (53 - 32) // Bit 53 (word 1 bit 21)
	Fattr4TimeModifySet  uint32 = 1 << (54 - 32)
	Fattr4MountedOnFileid uint32 = 1 << (55 - 32)
)
