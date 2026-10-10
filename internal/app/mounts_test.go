package app

import (
	"strings"
	"testing"

	"github.com/Ozzvin/equinox/internal/api"
)

// What a container on Umbrel shows in /proc/self/mountinfo: its root, the system's mounts, Docker's single files, the
// state folder, Downloads (a given place) and a folder the user added in the app's settings.
const sampleMountinfo = `1083 935 0:265 / / rw,relatime master:410 - overlay overlay rw,lowerdir=/x
1084 1083 0:268 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
1085 1083 0:269 / /dev rw,nosuid - tmpfs tmpfs rw,size=65536k,mode=755
1090 1083 8:2 /umbrel/app-data/ozzvin-equinox/data /data rw,relatime - ext4 /dev/sda2 rw
1091 1083 8:2 /umbrel/home/Downloads /downloads rw,relatime - ext4 /dev/sda2 rw
1092 1083 9:0 /Torrents /storage rw,relatime - ext4 /dev/md0 rw
1093 1083 9:0 /Films\040HD /External/RAID/my\040films rw,relatime - ext4 /dev/md0 rw
1094 1083 8:2 /docker/containers/abc/resolv.conf /etc/resolv.conf rw,relatime - ext4 /dev/sda2 rw
1095 1083 8:2 /docker/containers/abc/hostname /hostname-file rw,relatime - ext4 /dev/sda2 rw
1096 1084 0:270 / /proc/sys ro - proc proc ro
1097 1091 8:2 /umbrel/home/Downloads/x /downloads/x rw - ext4 /dev/sda2 rw
`

func TestAddedMounts(t *testing.T) {
	isDir := func(p string) bool { return p != "/hostname-file" }
	got := addedMounts(strings.NewReader(sampleMountinfo), "/data", []api.Place{{Name: "Downloads", Path: "/downloads"}}, isDir)
	want := []api.Place{{Name: "storage", Path: "/storage"}, {Name: "External/RAID/my films", Path: "/External/RAID/my films"}} // the whole mount point: as in Files
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}
