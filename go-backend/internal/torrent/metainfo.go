// Package torrent contains the small metainfo reader needed by the collection
// workflow. It intentionally keeps the bencode bytes for the info dictionary
// so the calculated hash is the BitTorrent info hash, not a re-encoded map.
package torrent

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

type File struct {
	Path   string
	Length int64
}

type Metainfo struct {
	Name     string
	InfoHash string
	Files    []File
}

type node struct {
	kind byte
	raw  []byte
	text []byte
	int  int64
	list []node
	dict map[string]node
}

type decoder struct {
	data []byte
	pos  int
}

func Parse(data []byte) (Metainfo, error) {
	if len(data) == 0 {
		return Metainfo{}, errors.New("种子文件为空")
	}
	d := decoder{data: data}
	root, err := d.value()
	if err != nil {
		return Metainfo{}, fmt.Errorf("解析种子失败: %w", err)
	}
	if d.pos != len(data) || root.kind != 'd' {
		return Metainfo{}, errors.New("解析种子失败: 根节点不是字典")
	}
	info, ok := root.dict["info"]
	if !ok || info.kind != 'd' {
		return Metainfo{}, errors.New("种子缺少 info 字典")
	}
	name, err := textField(info.dict, "name.utf-8", "name")
	if err != nil {
		return Metainfo{}, err
	}
	if name == "" {
		return Metainfo{}, errors.New("种子名称为空")
	}
	if err := validatePath(name); err != nil {
		return Metainfo{}, err
	}
	result := Metainfo{Name: name, InfoHash: infoHash(info.raw)}
	if files, ok := info.dict["files"]; ok {
		if files.kind != 'l' {
			return Metainfo{}, errors.New("种子 files 字段格式异常")
		}
		for _, item := range files.list {
			if item.kind != 'd' {
				return Metainfo{}, errors.New("种子文件项格式异常")
			}
			length, ok := integerField(item.dict, "length")
			if !ok || length < 0 {
				return Metainfo{}, errors.New("种子文件大小异常")
			}
			memberPath, err := pathField(item.dict, "path.utf-8", "path")
			if err != nil {
				return Metainfo{}, err
			}
			result.Files = append(result.Files, File{Path: joinTorrentPath(name, memberPath), Length: length})
		}
	} else {
		length, ok := integerField(info.dict, "length")
		if !ok || length < 0 {
			return Metainfo{}, errors.New("单文件种子大小异常")
		}
		if err := validatePath(name); err != nil {
			return Metainfo{}, err
		}
		result.Files = []File{{Path: name, Length: length}}
	}
	if len(result.Files) == 0 {
		return Metainfo{}, errors.New("种子不包含文件")
	}
	return result, nil
}

func infoHash(raw []byte) string {
	sum := sha1.Sum(raw)
	return hex.EncodeToString(sum[:])
}

func textField(fields map[string]node, names ...string) (string, error) {
	for _, name := range names {
		if value, ok := fields[name]; ok {
			if value.kind != 's' {
				return "", fmt.Errorf("种子字段 %s 格式异常", name)
			}
			return decodeText(value.text), nil
		}
	}
	return "", errors.New("种子缺少名称")
}

func integerField(fields map[string]node, name string) (int64, bool) {
	value, ok := fields[name]
	return value.int, ok && value.kind == 'i'
}

func pathField(fields map[string]node, names ...string) (string, error) {
	for _, name := range names {
		if value, ok := fields[name]; ok {
			if value.kind != 'l' || len(value.list) == 0 {
				return "", fmt.Errorf("种子路径字段 %s 格式异常", name)
			}
			parts := make([]string, 0, len(value.list))
			for _, part := range value.list {
				if part.kind != 's' {
					return "", errors.New("种子路径组件格式异常")
				}
				parts = append(parts, decodeText(part.text))
			}
			joined := strings.Join(parts, "/")
			if err := validatePath(joined); err != nil {
				return "", err
			}
			return joined, nil
		}
	}
	return "", errors.New("种子缺少文件路径")
}

func joinTorrentPath(root, member string) string {
	// root and member have already been validated independently; slash is used
	// here because torrent paths are platform independent.
	return strings.TrimSuffix(root, "/") + "/" + member
}

func validatePath(value string) error {
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" || strings.ContainsRune(value, '\x00') || strings.HasPrefix(value, "/") {
		return errors.New("种子包含不安全路径")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("种子包含不安全路径")
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("种子包含不安全路径")
		}
	}
	return nil
}

func decodeText(value []byte) string {
	if utf8.Valid(value) {
		return string(value)
	}
	// Legacy torrents often omit UTF-8 metadata. Retaining the bytes gives the
	// same lossless behavior as Java's ISO-8859-1-to-UTF-8 conversion for the
	// common single-byte names without introducing a locale-dependent decoder.
	return string(value)
}

func (d *decoder) value() (node, error) {
	start := d.pos
	if d.pos >= len(d.data) {
		return node{}, errors.New("bencode 意外结束")
	}
	switch d.data[d.pos] {
	case 'i':
		d.pos++
		end := strings.IndexByte(string(d.data[d.pos:]), 'e')
		if end < 0 {
			return node{}, errors.New("整数未闭合")
		}
		end += d.pos
		if end == d.pos {
			return node{}, errors.New("整数为空")
		}
		var value int64
		if _, err := fmt.Sscan(string(d.data[d.pos:end]), &value); err != nil {
			return node{}, errors.New("整数格式异常")
		}
		d.pos = end + 1
		return node{kind: 'i', int: value, raw: d.data[start:d.pos]}, nil
	case 'l':
		d.pos++
		items := []node{}
		for d.pos < len(d.data) && d.data[d.pos] != 'e' {
			item, err := d.value()
			if err != nil {
				return node{}, err
			}
			items = append(items, item)
		}
		if d.pos >= len(d.data) {
			return node{}, errors.New("列表未闭合")
		}
		d.pos++
		return node{kind: 'l', list: items, raw: d.data[start:d.pos]}, nil
	case 'd':
		d.pos++
		fields := map[string]node{}
		for d.pos < len(d.data) && d.data[d.pos] != 'e' {
			key, err := d.value()
			if err != nil || key.kind != 's' {
				return node{}, errors.New("字典键格式异常")
			}
			if _, exists := fields[string(key.text)]; exists {
				return node{}, errors.New("字典包含重复键")
			}
			value, err := d.value()
			if err != nil {
				return node{}, err
			}
			fields[string(key.text)] = value
		}
		if d.pos >= len(d.data) {
			return node{}, errors.New("字典未闭合")
		}
		d.pos++
		return node{kind: 'd', dict: fields, raw: d.data[start:d.pos]}, nil
	default:
		if d.data[d.pos] < '0' || d.data[d.pos] > '9' {
			return node{}, fmt.Errorf("未知 bencode 类型 %q", d.data[d.pos])
		}
		colon := strings.IndexByte(string(d.data[d.pos:]), ':')
		if colon < 0 {
			return node{}, errors.New("字符串长度未闭合")
		}
		colon += d.pos
		var length int
		if _, err := fmt.Sscan(string(d.data[d.pos:colon]), &length); err != nil || length < 0 {
			return node{}, errors.New("字符串长度异常")
		}
		d.pos = colon + 1
		if length > len(d.data)-d.pos {
			return node{}, errors.New("字符串超出种子范围")
		}
		d.pos += length
		return node{kind: 's', text: d.data[colon+1 : d.pos], raw: d.data[start:d.pos]}, nil
	}
}
