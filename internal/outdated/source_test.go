package outdated

import "testing"

func TestModuleAddress(t *testing.T) {
	cases := []struct {
		source, host, addr string
		ok                 bool
	}{
		{"terraform-aws-modules/vpc/aws", publicHost, "terraform-aws-modules/vpc/aws", true},
		{"terraform-aws-modules/security-group/aws//modules/http-80", publicHost, "terraform-aws-modules/security-group/aws", true},
		{"registry.terraform.io/hashicorp/consul/aws", publicHost, "hashicorp/consul/aws", true},
		{"app.terraform.io/acme/network/aws", "app.terraform.io", "acme/network/aws", true},
		{"./modules/vpc", "", "", false},
		{"../shared", "", "", false},
		{"github.com/hashicorp/example", "", "", false},
		{"bitbucket.org/hashicorp/example/aws", "", "", false},
		{"git::https://example.com/vpc.git?ref=v1.2.0", "", "", false},
		{"https://example.com/vpc-module.zip", "", "", false},
		{"s3::https://s3-eu-west-1.amazonaws.com/bucket/vpc.zip", "", "", false},
		{"hashicorp/consul/AWS", "", "", false}, // target system is lowercase only
	}
	for _, c := range cases {
		host, addr, ok := moduleAddress(c.source)
		if ok != c.ok || host != c.host || addr != c.addr {
			t.Errorf("moduleAddress(%q) = %q, %q, %t; want %q, %q, %t",
				c.source, host, addr, ok, c.host, c.addr, c.ok)
		}
	}
}

func TestProviderAddress(t *testing.T) {
	cases := []struct {
		name, source, host, addr string
		ok                       bool
	}{
		{"aws", "hashicorp/aws", publicHost, "hashicorp/aws", true},
		{"aws", "Registry.Terraform.io/HashiCorp/AWS", publicHost, "hashicorp/aws", true},
		{"aws", "", publicHost, "hashicorp/aws", true},
		{"corp", "tf.example.com/corp/thing", "tf.example.com", "corp/thing", true},
		{"x", "too/many/parts/here", "", "", false},
	}
	for _, c := range cases {
		host, addr, ok := providerAddress(c.name, c.source)
		if ok != c.ok || host != c.host || addr != c.addr {
			t.Errorf("providerAddress(%q, %q) = %q, %q, %t", c.name, c.source, host, addr, ok)
		}
	}
}
