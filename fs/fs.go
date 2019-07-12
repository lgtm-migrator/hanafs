package fs

import (
	"sync"

	"github.com/Soontao/hanafs/hana"
	"github.com/billziss-gh/cgofuse/fuse"
)

// DefaultRemoteCacheSeconds duration
const DefaultRemoteCacheSeconds = 15

// HanaFS type
type HanaFS struct {
	fuse.FileSystemBase
	client    *hana.Client
	statCache *StatCache
	dirCache  *DirectoryCache
	// run async job recheck this
	notExistList []string
}

func (f *HanaFS) isNotExist(path string) bool {
	for _, f := range f.notExistList {
		if f == path {
			return true
		}
	}
	return false
}

func (f *HanaFS) notExist(path string) {
	if !f.isNotExist(path) {
		f.notExistList = append(f.notExistList, path)
	}
}

func (f *HanaFS) removeFromNotExist(path string) {
	if !f.isNotExist(path) {
		for i, notExistPath := range f.notExistList {
			if notExistPath == path {
				f.notExistList = append(f.notExistList[:i], f.notExistList[i+1:]...)
			}
		}
	}
}

func (f *HanaFS) Release(path string, fh uint64) int {
	return 0
}

func (f *HanaFS) Open(path string, flags int) (errc int, fh uint64) {
	return 0, 0
}

func (f *HanaFS) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64,
	fh uint64) int {

	dir, err := f.dirCache.GetDir(path)

	if err != nil {
		return -fuse.ENOENT
	}

	for name, childStat := range dir.children {

		fill(name, childStat, 0)

	}

	return 0
}

func (f *HanaFS) Getattr(path string, s *fuse.Stat_t, fh uint64) int {

	if f.isNotExist(path) {
		return -fuse.ENOENT
	}

	stat, err := f.statCache.GetStat(path)

	if err != nil {
		f.notExist(path)
		return -fuse.ENOENT
	}

	if stat.Uid == 0 {
		uid, gid, _ := fuse.Getcontext()
		stat.Uid = uid
		stat.Gid = gid
	}

	*s = *stat

	return 0

}

func (f *HanaFS) Read(path string, buff []byte, ofst int64, fh uint64) (n int) {
	contents, err := f.client.ReadFile(path)

	if err != nil {
		return -fuse.ENOENT
	}

	endofst := ofst + int64(len(buff))

	if endofst > int64(len(contents)) {
		endofst = int64(len(contents))
	}

	if endofst < ofst {
		return 0
	}

	n = copy(buff, contents[ofst:endofst])

	return
}

func (f *HanaFS) getDir(path string) (*CachedDirectory, error) {

	rt := &CachedDirectory{children: map[string]*fuse.Stat_t{}}
	dir, err := f.client.ReadDirectory(path)

	if err != nil {
		return nil, err
	}

	now := fuse.Now()
	uid, gid, _ := fuse.Getcontext()

	wg := sync.WaitGroup{}

	for _, hanaChild := range dir.Children {

		fsChildStat := &fuse.Stat_t{
			Flags: 0,
			Uid:   uid,
			Gid:   gid,
			Ctim:  now,
			Atim:  now,
			Mtim:  now,
		}

		// parallel requested
		if hanaChild.Directory {
			fsChildStat.Mode = fuse.S_IFDIR | fuse.S_IRWXU
		} else {
			fsChildStat.Mode = fuse.S_IFREG | fuse.S_IRWXU // Regular File.

			if fsChildStat.Size == 0 {
				if content, err := f.client.ReadFile(path); err == nil {
					fsChildStat.Size = int64(len(content))
				}
			}
		}

		nodeName := hanaChild.Name
		nodePath := hanaChild.ContentLocation

		rt.children[nodeName] = fsChildStat
		f.statCache.PreStatCacheSeconds(nodePath, fsChildStat, DefaultRemoteCacheSeconds)

	}

	wg.Wait()

	f.dirCache.PreDirectoryCacheSeconds(path, rt, DefaultRemoteCacheSeconds)

	return rt, nil
}

func (f *HanaFS) getStat(path string) (*fuse.Stat_t, error) {

	s := &fuse.Stat_t{}
	hanaStat, err := f.client.Stat(path)

	if err != nil {
		return nil, err
	}

	now := fuse.Now()
	uid, gid, _ := fuse.Getcontext()

	s.Gid = gid
	s.Uid = uid
	s.Ctim = now
	s.Atim = now
	s.Mtim = now

	if hanaStat.Directory {
		s.Mode = fuse.S_IFDIR | fuse.S_IRWXU
	} else {
		s.Mode = fuse.S_IFREG | fuse.S_IRWXU // Regular File.
		if s.Size == 0 {
			if content, err := f.client.ReadFile(path); err == nil {
				s.Size = int64(len(content))
			}
		}
	}

	f.statCache.PreStatCacheSeconds(path, s, DefaultRemoteCacheSeconds)

	return s, nil

}

func (f *HanaFS) Chflags(path string, flags uint32) (errc int) {
	return 0
}

func (f *HanaFS) Setcrtime(path string, tmsp fuse.Timespec) int {
	return 0
}

func (f *HanaFS) Setchgtime(path string, tmsp fuse.Timespec) int {
	return 0
}

var _ fuse.FileSystemChflags = (*HanaFS)(nil)
var _ fuse.FileSystemSetcrtime = (*HanaFS)(nil)
var _ fuse.FileSystemSetchgtime = (*HanaFS)(nil)

// NewHanaFS type
func NewHanaFS(client *hana.Client) *HanaFS {
	fs := &HanaFS{client: client}
	fs.statCache = &StatCache{
		cache:    map[string]*fuse.Stat_t{},
		provider: fs.getStat,
	}
	fs.dirCache = &DirectoryCache{
		cache:    map[string]*CachedDirectory{},
		provider: fs.getDir,
	}
	return fs
}
