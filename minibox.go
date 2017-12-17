package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

/*
#cgo CFLAGS: -std=c99

#define _GNU_SOURCE

#include <linux/loop.h>
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/syscall.h>
#include <unistd.h>

void execWrapper(char* path, int argc, char *argstr) {
  char** argv = malloc((argc+2) * sizeof(char*));
  argv[0] = path;
  for (int i = 0; i < argc; i++) {
    argv[i+1] = argstr;
    argstr += strlen(argstr) + 1;
  }
  argv[argc+1] = NULL;
  execve(path, argv, NULL);
  free(argv);
  perror("execv");
  exit(1);
}
*/
import "C"

var (
	CLONE_FS    = int(C.CLONE_FS)
	CLONE_NEWNS = int(C.CLONE_NEWNS)

	LOOP_CTL_GET_FREE = uint(C.LOOP_CTL_GET_FREE)
	LOOP_SET_FD       = uint(C.LOOP_SET_FD)
	LOOP_CLR_FD       = uint(C.LOOP_CLR_FD)

	MNT_DETACH = int(C.MNT_DETACH)
)

func main() {
	log.SetFlags(0)
	exitCode, err := run(os.Args)
	if err != nil {
		log.Print(err)
	}
	os.Exit(exitCode)
}

func run(args []string) (exitCode int, err error) {
	closeFn := func(name string, fd int) {
		if closeErr := unix.Close(fd); err == nil && closeErr != nil {
			err = errors.Wrapf(closeErr, "close %s", name)
		}
	}

	flags := flag.NewFlagSet(args[0], flag.ExitOnError)
	dirFlag := flags.String("dir", "", "the mount point for the container")
	imageFlag := flags.String("image", "", "the container image to open")
	fstypeFlag := flags.String("fstype", "", "the file system type of the container")
	entryFlag := flags.String("entry", "", "name of the binary to execute within the container")
	uidFlag := flags.Int("uid", -1, "the uid under which the binary should be run")
	gidFlag := flags.Int("gid", -1, "the gid under which the binary should be run")
	if err := flags.Parse(args[1:]); err != nil {
		return 1, err
	}
	dir := *dirFlag
	image := *imageFlag
	fstype := *fstypeFlag
	entry := *entryFlag
	uid := *uidFlag
	gid := *gidFlag
	if dir == "" || image == "" || fstype == "" || entry == "" || uid < 0 || gid < 0 {
		flags.PrintDefaults()
		os.Exit(1)
	}

	if err := unix.Unshare(CLONE_FS | CLONE_NEWNS); err != nil {
		return 1, errors.Wrap(err, "unshare")
	}

	loopctlFd, err := unix.Open("/dev/loop-control", syscall.O_RDWR, 0)
	if err != nil {
		return 1, errors.Wrapf(err, "open /dev/loop-control", err)
	}
	defer closeFn("/dev/loop-control", loopctlFd)

	devNum, err := unix.IoctlGetInt(loopctlFd, LOOP_CTL_GET_FREE)
	if err != nil {
		return 1, errors.Wrap(err, "ioctl LOOP_CTL_GET_FREE")
	}

	loopDevName := fmt.Sprintf("/dev/loop%d", devNum)
	loopFd, err := unix.Open(loopDevName, syscall.O_RDWR, 0)
	if err != nil {
		return 1, errors.Wrapf(err, "open %s", loopDevName)
	}
	defer closeFn(loopDevName, loopFd)

	imageFd, err := unix.Open(image, syscall.O_RDWR, 0)
	if err != nil {
		return 1, errors.Wrapf(err, "open %s", image)
	}
	defer closeFn(image, imageFd)

	if err := unix.IoctlSetInt(loopFd, LOOP_SET_FD, imageFd); err != nil {
		return 1, errors.Wrap(err, "ioctl LOOP_SET_FD")
	}
	defer func() {
		if _, clearErr := unix.IoctlGetInt(loopFd, LOOP_CLR_FD); clearErr != nil && err == nil {
			err = clearErr
		}
	}()

	if err := unix.Mount(loopDevName, dir, fstype, 0, ""); err != nil {
		return 1, errors.Wrap(err, "mount")
	}

	pid, _, _ := unix.RawSyscall(uintptr(C.SYS_fork), 0, 0, 0)
	if pid < 0 {
		return 1, errors.New("fork")
	}
	if pid == 0 {
		oldRootDir := filepath.Join(dir, ".old_root")
		os.Mkdir(oldRootDir, 0700)
		if err := unix.PivotRoot(dir, oldRootDir); err != nil {
			log.Fatal(errors.Wrap(err, "pivot_root"))
		}
		if err := os.Chdir("/"); err != nil {
			log.Fatal(errors.Wrap(err, "chdir"))
		}
		if err := unix.Unmount("/.old_root", MNT_DETACH); err != nil {
			log.Fatal(errors.Wrap(err, "unmount"))
		}
		if err := os.Remove("/.old_root"); err != nil {
			log.Fatal(errors.Wrap(err, "remove"))
		}

		ret, _, _ := unix.RawSyscall(uintptr(C.SYS_setgid), uintptr(gid), 0, 0)
		if ret < 0 {
			log.Fatal("setgid")
		}

		ret, _, _ = unix.RawSyscall(uintptr(C.SYS_setuid), uintptr(uid), 0, 0)
		if ret < 0 {
			log.Fatal("setuid")
		}

		cEntry := C.CString(entry)
		cArgc := C.int(len(flag.Args()))
		cArgstr := C.CString(strings.Join(flag.Args(), "\x00") + "\x00")
		C.execWrapper(cEntry, cArgc, cArgstr)
	}

	for {
		var status unix.WaitStatus
		if _, err = unix.Wait4(int(pid), &status, 0, nil); err != nil {
			return 1, errors.Wrap(err, "wait4")
		}
		if status.Signaled() {
			return 1, errors.Errorf("process terminated by signal %v", status.Signal())
		}
		if status.Exited() {
			return status.ExitStatus(), nil
		}
		if status.Stopped() || status.Continued() {
			continue
		}
		return 1, errors.Errorf("unknown return from wait: %x", status)
	}
}
