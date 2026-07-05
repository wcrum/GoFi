package deploymode

import "testing"

func TestDetect(t *testing.T) {
	// Filesystem checks stubbed off by default so tests are hermetic and
	// don't depend on whether they happen to run inside a container.
	origDockerEnv := dockerEnvExists
	origCgroup := cgroupIndicatesContainer
	dockerEnvExists = func() bool { return false }
	cgroupIndicatesContainer = func() bool { return false }
	t.Cleanup(func() {
		dockerEnvExists = origDockerEnv
		cgroupIndicatesContainer = origCgroup
	})

	tests := []struct {
		name string
		env  map[string]string
		want Mode
	}{
		{
			name: "explicit override binary",
			env:  map[string]string{"GOFI_DEPLOY_MODE": "binary"},
			want: ModeBinary,
		},
		{
			name: "explicit override compose",
			env:  map[string]string{"GOFI_DEPLOY_MODE": "compose"},
			want: ModeCompose,
		},
		{
			name: "explicit override kubernetes",
			env:  map[string]string{"GOFI_DEPLOY_MODE": "kubernetes"},
			want: ModeKubernetes,
		},
		{
			name: "explicit override wins over k8s auto-detect",
			env: map[string]string{
				"GOFI_DEPLOY_MODE":         "binary",
				"KUBERNETES_SERVICE_HOST": "10.0.0.1",
			},
			want: ModeBinary,
		},
		{
			name: "invalid override falls through to auto-detect",
			env: map[string]string{
				"GOFI_DEPLOY_MODE":         "nonsense",
				"KUBERNETES_SERVICE_HOST": "10.0.0.1",
			},
			want: ModeKubernetes,
		},
		{
			name: "kubernetes auto-detect",
			env:  map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"},
			want: ModeKubernetes,
		},
		{
			name: "compose auto-detect via project name",
			env:  map[string]string{"COMPOSE_PROJECT_NAME": "gofi"},
			want: ModeCompose,
		},
		{
			name: "compose auto-detect via compose file",
			env:  map[string]string{"COMPOSE_FILE": "docker-compose.yml"},
			want: ModeCompose,
		},
		{
			name: "default binary",
			env:  map[string]string{},
			want: ModeBinary,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{
				"GOFI_DEPLOY_MODE",
				"KUBERNETES_SERVICE_HOST",
				"COMPOSE_PROJECT_NAME",
				"COMPOSE_FILE",
			} {
				if v, ok := tt.env[k]; ok {
					t.Setenv(k, v)
				} else {
					t.Setenv(k, "")
				}
			}

			if got := Detect(); got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetect_ContainerFallback(t *testing.T) {
	t.Setenv("GOFI_DEPLOY_MODE", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	t.Setenv("COMPOSE_FILE", "")

	origDockerEnv := dockerEnvExists
	origCgroup := cgroupIndicatesContainer
	t.Cleanup(func() {
		dockerEnvExists = origDockerEnv
		cgroupIndicatesContainer = origCgroup
	})

	dockerEnvExists = func() bool { return true }
	cgroupIndicatesContainer = func() bool { return false }
	if got := Detect(); got != ModeDocker {
		t.Errorf("Detect() with dockerenv = %q, want %q", got, ModeDocker)
	}

	dockerEnvExists = func() bool { return false }
	cgroupIndicatesContainer = func() bool { return true }
	if got := Detect(); got != ModeDocker {
		t.Errorf("Detect() with cgroup indicator = %q, want %q", got, ModeDocker)
	}
}
