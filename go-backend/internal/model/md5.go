package model

import "crypto/md5"

func md5Digest(input []byte) [16]byte { return md5.Sum(input) }
