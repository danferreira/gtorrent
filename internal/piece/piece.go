package piece

type PieceWork struct {
	Index  int
	Hash   [20]byte
	Length int
}

type PieceDownloaded struct {
	Index int
	Data  []byte
	PW    PieceWork
}
