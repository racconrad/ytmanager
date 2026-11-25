package ytdlp

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func ownsStageRoot(root *os.Root, _ os.FileInfo) bool {
	dir, err := root.Open(".")
	if err != nil {
		return false
	}
	defer dir.Close()
	desc, err := windows.GetSecurityInfo(windows.Handle(dir.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	owner, _, err := desc.Owner()
	if err != nil || owner == nil {
		return false
	}
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	return err == nil && (owner.Equals(user.User.Sid) || token.IsElevated() && owner.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

func tryStageLock(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}
