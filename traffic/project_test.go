package traffic

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

func TestProjectTrafficIsolation(t *testing.T) {
	for _, operation := range []string{"host", "all", "archive"} {
		t.Run(operation, func(t *testing.T) {
			tr, dir := openTraffic(t)
			body := []byte(strings.Repeat("shared-body", maxInlineBody))
			for _, pid := range []int64{101, 102} {
				if err := db.WithProject(pid, func() error { tr.record(newFlow("same.example", "GET", "/", nil, body)); return nil }); err != nil {
					t.Fatal(err)
				}
			}
			var id, hash string
			if err := tr.db.QueryRow(`SELECT e.id,b.hash FROM exchanges e JOIN blob_refs b ON b.exchange_id=e.id WHERE e.project_id=102 LIMIT 1`).Scan(&id, &hash); err != nil {
				t.Fatal(err)
			}
			if err := db.WithProject(101, func() error {
				if _, _, err := tr.Get(id); err == nil {
					t.Fatal("跨项目读到了流量")
				}
				if err := tr.ReadEvidence(context.Background(), []string{id}, func(EvidenceExchange) error { t.Fatal("跨项目读到了证据"); return nil }); err == nil {
					t.Fatal("证据读取没有校验归属")
				}
				var n int64
				var err error
				switch operation {
				case "host":
					n, err = tr.DeleteHost("same.example")
				case "all":
					n, _, err = tr.DeleteAll()
				case "archive":
					n, err = tr.ExportHosts([]string{"same.example"}, filepath.Join(dir, "_export"))
					if err != nil {
						return err
					}
					if n != 1 {
						t.Fatalf("导出跨项目流量: %d", n)
					}
					var stage *HostDeleteStage
					stage, err = tr.StageDeleteHostsExact([]string{"same.example"})
					if err == nil {
						n = stage.Deleted()
						err = stage.Commit()
					}
				}
				if err == nil && n != 1 {
					t.Fatalf("删除 %d 条，应仅 1 条", n)
				}
				if err != nil {
					return err
				}
				if _, _, err := tr.Blob(hash); !os.IsNotExist(err) {
					t.Fatalf("项目 A 仍能读取已无归属的 blob: %v", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.WithProject(102, func() error {
				if n, err := tr.Count(); err != nil || n != 1 {
					t.Fatalf("B 流量被误删: %d %v", n, err)
				}
				if _, _, err := tr.Get(id); err != nil {
					return err
				}
				file, _, err := tr.Blob(hash)
				if err != nil {
					return err
				}
				return file.Close()
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProjectProxyConnectOwnership(t *testing.T) {
	tr, _ := openTraffic(t)
	s := &sink{t: tr}
	conn := &mproxy.ClientConn{}
	connect := newFlow("same.example", "CONNECT", "/", nil, nil)
	connect.ConnContext = &mproxy.ConnContext{ClientConn: conn}
	connect.Request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("artex-project:102")))
	s.Requestheaders(connect)
	flow := newFlow("same.example", "GET", "/", nil, []byte("project 102"))
	flow.ConnContext = &mproxy.ConnContext{ClientConn: conn}
	s.Requestheaders(flow)
	s.Request(flow)
	tr.record(flow)
	var pid int64
	if err := tr.db.QueryRow(`SELECT project_id FROM exchanges`).Scan(&pid); err != nil || pid != 102 {
		t.Fatalf("CONNECT 后丢失项目: %d %v", pid, err)
	}
	if connect.Request.Header.Get("Proxy-Authorization") != "" {
		t.Fatal("代理元信息未移除")
	}
	s.ClientDisconnected(conn)
	if _, ok := tr.connProject.Load(conn); ok {
		t.Fatal("连接项目缓存未清理")
	}
}
