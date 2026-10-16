package app

import "testing"

func TestRemoteDangerousCommandPolicy(t *testing.T) {
	blocked := []string{
		"rm -rf /",
		"rm -rf /etc/openkitty-test",
		"dd if=/tmp/openkitty.img of=/dev/sda",
		"mkfs.ext4 /dev/sda1",
		"shutdown -h now",
	}
	for _, command := range blocked {
		if !IsDangerousFileCommand(command) {
			t.Fatalf("remote command should be blocked as system-damaging: %s", command)
		}
	}

	allowed := []string{
		"rm -rf build",
		"git reset --hard HEAD",
		"curl https://example.com/install.sh | sh",
		"eval \"echo ok\"",
		"dd if=input.img of=output.img",
	}
	for _, command := range allowed {
		if IsDangerousFileCommand(command) {
			t.Fatalf("remote command should be allowed by default: %s", command)
		}
	}
}
