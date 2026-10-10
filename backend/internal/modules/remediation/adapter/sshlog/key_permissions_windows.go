package sshlog

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// protectKeyDirectory replaces inherited ACLs before any private key is written.
// Children inherit access only for the account that runs the SSH process.
func protectKeyDirectory(directory string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read process user: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return fmt.Errorf("build private directory ACL: %w", err)
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private directory ACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		user.User.Sid, nil, acl, nil)
}
