package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDoctorParallelTwoDoctorsWithDifferentHomesDoNotSeeEachOther: when two doctorEnv values
// are built with different home directories, each resolves its own local binary path
// and the other's home is invisible. This pins the homeDir seam as a field.
func TestDoctorParallelTwoDoctorsWithDifferentHomesDoNotSeeEachOther(t *testing.T) {
	t.Parallel()
	home1 := "/home/one"
	home2 := "/home/two"
	pathBin := "/usr/local/bin/nova-swarm"
	localBin1 := filepath.Join(home1, ".local", "bin", "nova-swarm")
	localBin2 := filepath.Join(home2, ".local", "bin", "nova-swarm")
	stamp := "nova-swarm 20261010000000-aaaaaaaaaaaa linux/amd64 go1.26.5"

	cases := []struct {
		name string
		env  doctorEnv
		want string
	}{
		{"home1 sees its local", doctorEnv{
			lookPath: func(name string) (string, error) {
				if name == "nova-swarm" {
					return pathBin, nil
				}
				return "", errors.New("not found")
			},
			homeDir: func() (string, error) { return home1, nil },
			read: func(path string) (string, error) {
				if path == localBin1 || path == pathBin {
					return stamp, nil
				}
				return "", errDoctorNotFound
			},
		}, stamp},
		{"home2 sees its local", doctorEnv{
			lookPath: func(name string) (string, error) {
				if name == "nova-swarm" {
					return pathBin, nil
				}
				return "", errors.New("not found")
			},
			homeDir: func() (string, error) { return home2, nil },
			read: func(path string) (string, error) {
				if path == localBin2 || path == pathBin {
					return stamp, nil
				}
				return "", errDoctorNotFound
			},
		}, stamp},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			code := c.env.cmdDoctor(nil, &out, &errOut)
			require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
			assert.Contains(t, out.String(), "DOCTOR OK stamp="+c.want)
		})
	}
}

// TestDoctorParallelAMissingToolIsNamedFromTheFakeLookPath pins the lookPath seam as a field.
func TestDoctorParallelAMissingToolIsNamedFromTheFakeLookPath(t *testing.T) {
	t.Parallel()
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return "", errors.New("not found")
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/home/me", nil },
		read:    func(path string) (string, error) { return "", errDoctorNotFound },
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0", code)
	assert.Contains(t, out.String(), "DOCTOR OK nothing to compare")
}

// TestDoctorParallelTwoPathVersionsWithNoLocalBinary pins lookPath without resolving home.
func TestDoctorParallelTwoPathVersionsWithNoLocalBinary(t *testing.T) {
	t.Parallel()
	stamp := "nova-swarm 20261010000000-aaaaaaaaaaaa linux/amd64 go1.26.5"
	pathBin := "/tmp/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/nonexistent", errors.New("no home") },
		read:    func(path string) (string, error) { return stamp, nil },
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0", code)
	assert.Contains(t, out.String(), "DOCTOR OK stamp="+stamp)
}

// TestDoctorParallelHomeIsInjected pins homeDir as a field that controls local binary path.
func TestDoctorParallelHomeIsInjected(t *testing.T) {
	t.Parallel()
	stamp := "nova-swarm 20261010000000-bbbbbbbbbbbb linux/amd64 go1.26.5"
	pathBin := "/usr/bin/nova-swarm"
	localBin := "/custom/home/.local/bin/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/custom/home", nil },
		read: func(path string) (string, error) {
			if path == localBin || path == pathBin {
				return stamp, nil
			}
			return "", errDoctorNotFound
		},
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0", code)
	assert.Contains(t, out.String(), "DOCTOR OK stamp="+stamp)
}

// TestDoctorParallelLookPathIsSeam pins that lookPath controls the PATH resolution.
func TestDoctorParallelLookPathIsSeam(t *testing.T) {
	t.Parallel()
	stamp := "nova-swarm 20261010000000-cccccccccccc linux/amd64 go1.26.5"
	pathBin := "/my/path/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/home/me", nil },
		read: func(path string) (string, error) {
			if path == pathBin {
				return stamp, nil
			}
			return "", errDoctorNotFound
		},
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0", code)
	assert.Contains(t, out.String(), "DOCTOR OK stamp="+stamp)
}

// TestDoctorParallelReadIsSeam pins that read controls the version resolution.
func TestDoctorParallelReadIsSeam(t *testing.T) {
	t.Parallel()
	stamp := "nova-swarm 20261010000000-dddddddddddd linux/amd64 go1.26.5"
	pathBin := "/usr/local/bin/nova-swarm"
	localBin := "/home/me/.local/bin/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/home/me", nil },
		read: func(path string) (string, error) {
			if path == localBin || path == pathBin {
				return stamp, nil
			}
			return "", errDoctorNotFound
		},
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0", code)
	assert.Contains(t, out.String(), "DOCTOR OK stamp="+stamp)
}

// TestDoctorParallelAllSeamsInjected: a full env with all seams injected for parallel tests.
func TestDoctorParallelAllSeamsInjected(t *testing.T) {
	t.Parallel()
	stamp := "nova-swarm 20261010000000-eeeeeeeeeeee linux/amd64 go1.26.5"
	pathBin := "/bin/nova-swarm"
	localBin := "/test/home/.local/bin/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/test/home", nil },
		read: func(path string) (string, error) {
			if path == localBin || path == pathBin {
				return stamp, nil
			}
			return "", errDoctorNotFound
		},
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0", code)
	assert.Contains(t, out.String(), "DOCTOR OK stamp="+stamp)
}

// TestDoctorParallelPathOverrideSkipsLookPath pins the --path flag behavior.
func TestDoctorParallelPathOverrideSkipsLookPath(t *testing.T) {
	t.Parallel()
	stamp := "nova-swarm 20261010000000-ffffffffffff linux/amd64 go1.26.5"
	pathBin := "/override/path/nova-swarm"
	localBin := "/home/me/.local/bin/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return "/actual/path/nova-swarm", nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/home/me", nil },
		read: func(path string) (string, error) {
			if path == localBin || path == pathBin {
				return stamp, nil
			}
			return "", errDoctorNotFound
		},
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", pathBin, "--local", localBin}, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0", code)
	assert.Contains(t, out.String(), "DOCTOR OK stamp="+stamp)
}

// TestDoctorParallelLocalOverridePinsHomeDir pins homeDir.
func TestDoctorParallelLocalOverridePinsHomeDir(t *testing.T) {
	t.Parallel()
	stamp := "nova-swarm 20261010000000-000000000000 linux/amd64 go1.26.5"
	pathBin := "/usr/bin/nova-swarm"
	localBin := "/override/local/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/home/me", nil },
		read: func(path string) (string, error) {
			if path == localBin || path == pathBin {
				return stamp, nil
			}
			return "", errDoctorNotFound
		},
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--local", localBin}, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0", code)
	assert.Contains(t, out.String(), "DOCTOR OK stamp="+stamp)
}

// TestDoctorParallelShadowedBinaryWithInjectedSeams pins the shadow detection with seams.
func TestDoctorParallelShadowedBinaryWithInjectedSeams(t *testing.T) {
	t.Parallel()
	stampPath := "nova-swarm 20261010000000-aaaaaaaaaaaa linux/amd64 go1.26.5"
	stampLocal := "nova-swarm 20261010000000-bbbbbbbbbbbb linux/amd64 go1.26.5"
	pathBin := "/usr/bin/nova-swarm"
	localBin := "/home/me/.local/bin/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/home/me", nil },
		read: func(path string) (string, error) {
			if path == pathBin {
				return stampPath, nil
			}
			if path == localBin {
				return stampLocal, nil
			}
			return "", errDoctorNotFound
		},
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 2, code, "exit %d, want 2 (shadowed)", code)
	assert.Contains(t, errOut.String(), stampPath)
	assert.Contains(t, errOut.String(), stampLocal)
}

// TestDoctorParallelUnreadableBinaryWithInjectedSeams pins unreadable binary handling with seams.
func TestDoctorParallelUnreadableBinaryWithInjectedSeams(t *testing.T) {
	t.Parallel()
	stampLocal := "nova-swarm 20261010000000-aaaaaaaaaaaa linux/amd64 go1.26.5"
	pathBin := "/nonexistent/nova-swarm"
	localBin := "/home/me/.local/bin/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/home/me", nil },
		read: func(path string) (string, error) {
			if path == localBin {
				return stampLocal, nil
			}
			return "", errors.New("exited 1")
		},
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 2, code, "exit %d, want 2 (unreadable)", code)
	assert.Contains(t, errOut.String(), "DOCTOR UNREADABLE")
	assert.Contains(t, errOut.String(), pathBin)
}

// TestDoctorParallelPreflightLaunch withInjectedSeams pins preflight with seams.
func TestDoctorParallelPreflightLaunchWithInjectedSeams(t *testing.T) {
	t.Parallel()
	stampPath := "nova-swarm 20261010000000-aaaaaaaaaaaa linux/amd64 go1.26.5"
	stampLocal := "nova-swarm 20261010000000-bbbbbbbbbbbb linux/amd64 go1.26.5"
	pathBin := "/usr/bin/nova-swarm"
	localBin := "/home/me/.local/bin/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/home/me", nil },
		read: func(path string) (string, error) {
			if path == pathBin {
				return stampPath, nil
			}
			if path == localBin {
				return stampLocal, nil
			}
			return "", errDoctorNotFound
		},
		run: func(binary string, args ...string) (line string, rc int) {
			return "ok", 0
		},
	}
	var errOut bytes.Buffer
	code, stop := env.preflight([]string{"native", "--tokens", "1"}, &errOut)
	require.True(t, stop)
	require.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), stampPath)
	assert.Contains(t, errOut.String(), stampLocal)
}

// TestDoctorParallelPreflightHelpStandsAside pins help requests with seams.
func TestDoctorParallelPreflightHelpStandsAside(t *testing.T) {
	t.Parallel()
	env := doctorEnv{
		lookPath: noPath,
		homeDir:  func() (string, error) { return "/home/me", nil },
		read: func(path string) (string, error) {
			return "", errDoctorNotFound
		},
		run: func(binary string, args ...string) (line string, rc int) { return "ok", 0 },
	}
	var errOut bytes.Buffer
	code, stop := env.preflight([]string{"native", "-h"}, &errOut)
	require.False(t, stop)
	require.Equal(t, 0, code)
}

// TestDoctorParallelSameFileNoComparison pins same binary file detection with seams.
func TestDoctorParallelSameFileNoComparison(t *testing.T) {
	t.Parallel()
	stamp := "nova-swarm 20261010000000-aaaaaaaaaaaa linux/amd64 go1.26.5"
	pathBin := "/home/me/.local/bin/nova-swarm"
	env := doctorEnv{
		lookPath: func(name string) (string, error) {
			if name == "nova-swarm" {
				return pathBin, nil
			}
			return "", errors.New("not found")
		},
		homeDir: func() (string, error) { return "/home/me", nil },
		read:    func(path string) (string, error) { return stamp, nil },
	}
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 0, code)
	assert.Contains(t, out.String(), "DOCTOR OK stamp="+stamp)
}
