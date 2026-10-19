package proxy

import (
	"reflect"
	"testing"
)

func TestValidateRemoteHost(t *testing.T) {
	valid := []string{"10.0.0.1", "db.example.com", "my-host", "[2001:db8::1]", "[::1]", "192.168.1.100"}
	for _, host := range valid {
		if err := ValidateRemoteHost(host); err != nil {
			t.Errorf("%q 应合法: %v", host, err)
		}
	}
	invalid := []string{"", "host with space", "host;rm -rf", "host`id`", "host$(id)", "høst"}
	for _, host := range invalid {
		if err := ValidateRemoteHost(host); err == nil {
			t.Errorf("%q 应被拒绝", host)
		}
	}
}

func TestSocatArgs(t *testing.T) {
	got := SocatArgs(15201, "10.0.0.2", 3306)
	want := []string{
		"TCP-LISTEN:15201,bind=0.0.0.0,fork,reuseaddr",
		"TCP:10.0.0.2:3306",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("socat 参数不符:\ngot  %v\nwant %v", got, want)
	}
}
