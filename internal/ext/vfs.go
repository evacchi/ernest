package ext

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"

	"tractor.dev/wanix/fs/cowfs"
	"tractor.dev/wanix/fs/fskit"
	"tractor.dev/wanix/fs/localfs"
	"tractor.dev/wanix/fs/memfs"
	"tractor.dev/wanix/fs/pipe"
)

const (
	dirWork    = "work"
	dirAgent   = "agent"
	dirConfig  = "config"
	fileRPC    = "rpc"
	fileModel  = "model"
	fileHist   = "history"
	readOnly   = 0o444
	readWrite  = 0o644
	blockingIO = true
)

// namespace builds the guest's whole file system:
//
//	/work                host workdir behind a copy-on-write overlay (writes never reach disk)
//	/agent/rpc           guest end of the RPC pipe
//	/agent/history       conversation so far, JSON
//	/agent/config/model  current model; writing it switches the model
func namespace(workdir string, s Session, rpc *pipe.PortFile) (fs.FS, error) {
	base, err := localfs.New(workdir)
	if err != nil {
		return nil, err
	}

	return fskit.MapFS{
		dirWork: &cowfs.FS{Base: base, Overlay: memfs.New()},
		dirAgent: fskit.MapFS{
			fileRPC:  fskit.OpenFunc(func(context.Context, string) (fs.File, error) { return rpc, nil }),
			fileHist: computed(fileHist, func() ([]byte, error) { return json.Marshal(s.History()) }),
			dirConfig: fskit.MapFS{
				fileModel: setting(fileModel, s.Model, s.SetModel),
			},
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

// setting is a read-write control file: reads return get(), and the
// content written during one open is passed to set() on close, e.g.
//
//	echo gpt-5 > /agent/config/model
func setting(name string, get func() string, set func(string) error) fs.FS {
	return fskit.OpenFunc(func(context.Context, string) (fs.File, error) {
		read := false
		return &fskit.FuncFile{
			Node: fskit.Entry(name, readWrite),
			ReadFunc: func(n *fskit.Node) error {
				read = true
				fskit.SetData(n, []byte(get()+"\n"))
				return nil
			},
			CloseFunc: func(n *fskit.Node) error {
				if read || len(n.Data()) == 0 {
					return nil
				}
				return set(strings.TrimSpace(string(n.Data())))
			},
		}, nil
	})
}

// newPipe returns the host and guest ends of a blocking duplex pipe.
func newPipe() (host, guest *pipe.PortFile) {
	h, g := pipe.New(blockingIO)
	return &pipe.PortFile{Port: h, Name: fileRPC}, &pipe.PortFile{Port: g, Name: fileRPC}
}
