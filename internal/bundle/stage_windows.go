//go:build windows

package bundle

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// stageTrustees are the only principals that may own, or hold any right to
// change, a staging directory an elevated run hands to pnputil. The same list
// builds the DACL a new directory is created with, so the two cannot drift.
// Tests append their own user so the checks can run unelevated.
var stageTrustees = wellKnownSIDs(windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid)

func wellKnownSIDs(types ...windows.WELL_KNOWN_SID_TYPE) []*windows.SID {
	sids := make([]*windows.SID, 0, len(types))
	for _, t := range types {
		sid, err := windows.CreateWellKnownSid(t)
		if err != nil {
			panic(fmt.Sprintf("bundle: well-known SID %d: %v", t, err))
		}
		sids = append(sids, sid)
	}
	return sids
}

// Every right that lets a principal change what is in the directory, or who
// may: write or append (add file or subdirectory), extended attributes and
// attributes, delete a child or the directory itself, rewrite the DACL or take
// ownership, and the generic rights that imply them.
const stageWriteMask = 0x2 | 0x4 | 0x10 | 0x40 | 0x100 | // FILE_WRITE_DATA, FILE_APPEND_DATA, FILE_WRITE_EA, FILE_DELETE_CHILD, FILE_WRITE_ATTRIBUTES
	windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL

func processElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func stageTrustee(sid *windows.SID) bool {
	for _, trusted := range stageTrustees {
		if sid.Equals(trusted) {
			return true
		}
	}
	return false
}

// createStageDir creates dir in one call with a protected DACL granting only
// the stage trustees, so there is no moment when it exists with inherited
// rights. It fails with an fs.ErrExist error if anything is already there.
func createStageDir(dir string) error {
	sddl := "D:P"
	for _, sid := range stageTrustees {
		sddl += "(A;OICI;FA;;;" + sid.String() + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: sd}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	return windows.CreateDirectory(path, &attributes)
}

// checkStageDir accepts dir only if no principal outside the stage trustees
// could have changed its contents: a real directory (not a junction or other
// reparse point), owned by a trustee, whose DACL grants no write-capable right
// to anyone else.
func checkStageDir(dir string) error {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(path)
	if err != nil {
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("it is a junction or other reparse point")
	}
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.New("it is not a directory")
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	return checkStageSecurity(sd)
}

func checkStageSecurity(sd *windows.SECURITY_DESCRIPTOR) error {
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !stageTrustee(owner) {
		return fmt.Errorf("it is owned by %s", owner)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return errors.New("it has no DACL, so everyone has full control")
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue // a deny entry can only take rights away
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return fmt.Errorf("its DACL has an entry of type %d SpoolSmith does not evaluate", ace.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Mask&stageWriteMask != 0 && !stageTrustee(sid) {
			return fmt.Errorf("%s can change it", sid)
		}
	}
	return nil
}
