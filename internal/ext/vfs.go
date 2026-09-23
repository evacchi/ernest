package ext

import (
	"context"
	"encoding/json"
	"io/fs"

	"tractor.dev/wanix/fs/cowfs"
	"tractor.dev/wanix/fs/fskit"
	"tractor.dev/wanix/fs/localfs"
	"tractor.dev/wanix/fs/memfs"
	"tractor.dev/wanix/fs/pipe"
)

const (
	dirWork    = "work"
	dirAgent   = "agent"
	fileRPC    = "rpc"
	fileModel  = "model"
	fileHist   = "history"
	readOnly   = 0o444
	blockingIO = true
)

// namespace builds the guest's whole file system:
//
//	/work         host workdir behind a copy-on-write overlay (writes never reach disk)
//	/agent/rpc    guest end of the RPC pipe
//	/agent/model  current model name
//	/agent/history conversation so far, JSON
func namespace(workdir string, s Session, rpc *pipe.PortFile) (fs.FS, error) {
	base, err := localfs.New(workdir)
	if err != nil {
		return nil, err
	}

	return fskit.MapFS{
		dirWork: &cowfs.FS{Base: base, Overlay: memfs.New()},
		dirAgent: fskit.MapFS{
			fileRPC:   fskit.OpenFunc(func(context.Context, string) (fs.File, error) { return rpc, nil }),
			fileModel: computed(fileModel, func() ([]byte, error) { return []byte(s.Model), nil }),
			fileHist:  computed(fileHist, func() ([]byte, error) { return json.Marshal(s.History()) }),
		},
	}, nil
}

// computed is a read-only file whose content fn produces on every read.
func computed(name string, fn func() ([]byte, error)) fs.FS {
	return fskit.OpenFunc(func(context.Context, string) (fs.File, error) {
		return &fskit.FuncFile{
			Node: fskit.Entry(name, readOnly),
			ReadFunc: func(n *fskit.Node) error {
				data, err := fn()
				if err != nil {
					return err
				}
				fskit.SetData(n, data)
				return nil
			},
		}, nil
	})
}

// newPipe returns the host and guest ends of a blocking duplex pipe.
func newPipe() (host, guest *pipe.PortFile) {
	h, g := pipe.New(blockingIO)
	return &pipe.PortFile{Port: h, Name: fileRPC}, &pipe.PortFile{Port: g, Name: fileRPC}
}
