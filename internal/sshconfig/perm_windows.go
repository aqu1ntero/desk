package sshconfig

// permOK is always true on Windows: ponytail: Win32-OpenSSH checks the
// file's ACL; add that when desk ships a Windows build that needs it.
func permOK(string) bool { return true }
