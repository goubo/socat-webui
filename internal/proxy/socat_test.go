package proxy

import (
	"reflect"
	"testing"
)

func TestNormalizeRemoteHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"10.0.0.1", "10.0.0.1"},
		{"db.example.com", "db.example.com"},
		{"my-host", "my-host"},
		{"[2001:db8::1]", "2001:db8::1"},
		{"2001:db8::1", "2001:db8::1"},
		{"[::1]", "::1"},
		{"192.168.1.100", "192.168.1.100"},
	}
	for _, c := range cases {
		got, err := NormalizeRemoteHost(c.in)
		if err != nil {
			t.Errorf("%q 应合法: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q 规范化不符: got %q want %q", c.in, got, c.want)
		}
	}
	invalid := []string{"", "host with space", "host;rm -rf", "host`id`", "host$(id)", "høst",
		"1.2.3.4:80", "1:2:3:4:5:6:7:8:9", "2001:db8::zzz"}
	for _, host := range invalid {
		if _, err := NormalizeRemoteHost(host); err == nil {
			t.Errorf("%q 应被拒绝", host)
		}
	}
}

func TestValidateFamilies(t *testing.T) {
	valid := []struct{ local, remote, host string }{
		{"", "", "10.0.0.1"},
		{"ipv4", "auto", "db.example.com"},
		{"ipv6", "ipv6", "::1"},
		{"dual", "auto", "[2001:db8::1]"},
		{"ipv4", "ipv6", "db.example.com"}, // 主机名强制 v6 解析合法
	}
	for _, c := range valid {
		host, _ := NormalizeRemoteHost(c.host)
		if err := ValidateFamilies(c.local, c.remote, host); err != nil {
			t.Errorf("%v 应合法: %v", c, err)
		}
	}
	invalid := []struct{ local, remote, host string }{
		{"ipx", "auto", "10.0.0.1"},
		{"ipv4", "ipx", "10.0.0.1"},
		{"ipv4", "ipv6", "10.0.0.1"}, // v4 字面量与强制 v6 冲突
		{"ipv6", "ipv4", "::1"},      // v6 字面量与强制 v4 冲突
	}
	for _, c := range invalid {
		host, _ := NormalizeRemoteHost(c.host)
		if err := ValidateFamilies(c.local, c.remote, host); err == nil {
			t.Errorf("%v 应被拒绝", c)
		}
	}
}

func TestSocatArgs(t *testing.T) {
	cases := []struct {
		name         string
		localPort    int
		localFamily  string
		remoteHost   string
		remotePort   int
		remoteFamily string
		want         []string
	}{
		{
			name: "默认等价历史行为", localPort: 15201, remoteHost: "10.0.0.2", remotePort: 3306,
			want: []string{
				"TCP4-LISTEN:15201,bind=0.0.0.0,fork,reuseaddr",
				"TCP4:10.0.0.2:3306",
			},
		},
		{
			name: "本地IPv6监听转IPv4远端（转换转发）", localPort: 15202, localFamily: "ipv6", remoteHost: "10.0.0.2", remotePort: 3306,
			want: []string{
				"TCP6-LISTEN:15202,bind=[::],fork,reuseaddr,ipv6only=1",
				"TCP4:10.0.0.2:3306",
			},
		},
		{
			name: "本地IPv4监听转IPv6远端（转换转发）", localPort: 15203, remoteHost: "2001:db8::2", remotePort: 3306,
			want: []string{
				"TCP4-LISTEN:15203,bind=0.0.0.0,fork,reuseaddr",
				"TCP6:[2001:db8::2]:3306",
			},
		},
		{
			name: "双栈监听", localPort: 15204, localFamily: "dual", remoteHost: "db.example.com", remotePort: 5432,
			want: []string{
				"TCP6-LISTEN:15204,bind=[::],fork,reuseaddr,ipv6only=0",
				"TCP:db.example.com:5432",
			},
		},
		{
			name: "主机名强制IPv4解析", localPort: 15205, remoteHost: "db.example.com", remotePort: 5432, remoteFamily: "ipv4",
			want: []string{
				"TCP4-LISTEN:15205,bind=0.0.0.0,fork,reuseaddr",
				"TCP4:db.example.com:5432",
			},
		},
		{
			name: "主机名强制IPv6解析", localPort: 15206, remoteHost: "db.example.com", remotePort: 5432, remoteFamily: "ipv6",
			want: []string{
				"TCP4-LISTEN:15206,bind=0.0.0.0,fork,reuseaddr",
				"TCP6:db.example.com:5432",
			},
		},
	}
	for _, c := range cases {
		got, err := SocatArgs(c.localPort, c.localFamily, c.remoteHost, c.remotePort, c.remoteFamily)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s 参数不符:\ngot  %v\nwant %v", c.name, got, c.want)
		}
	}
	if _, err := SocatArgs(15201, "ipx", "10.0.0.2", 3306, ""); err == nil {
		t.Error("非法本地协议族应报错")
	}
}

func TestFormatRemoteHost(t *testing.T) {
	if got := FormatRemoteHost("2001:db8::1", 443); got != "[2001:db8::1]:443" {
		t.Errorf("IPv6 应加括号: %q", got)
	}
	if got := FormatRemoteHost("10.0.0.2", 443); got != "10.0.0.2:443" {
		t.Errorf("IPv4 应原样: %q", got)
	}
	if got := FormatRemoteHost("db.example.com", 443); got != "db.example.com:443" {
		t.Errorf("主机名应原样: %q", got)
	}
}
