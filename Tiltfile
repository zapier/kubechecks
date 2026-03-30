load('ext://dotenv', 'dotenv')
load('ext://helm_remote', 'helm_remote')
load('ext://tests/golang', 'test_go')
load('ext://namespace', 'namespace_create')
load('ext://uibutton', 'cmd_button')
load('ext://helm_resource', 'helm_resource')
load('ext://local_output', 'local_output')
load('./.tilt/terraform/Tiltfile', 'local_terraform_resource')
load('./.tilt/utils/Tiltfile', 'check_env_set')

# /////////////////////////////////////////////////////////////////////////////
# S E C R E T S
# /////////////////////////////////////////////////////////////////////////////
# Secrets are loaded from the OS keychain (macOS Keychain / Linux pass).
# Fallback to .secret file for backwards compatibility.
#
# Setup (one-time):
#   macOS:  security add-generic-password -a "$USER" -s "kubechecks/<KEY_NAME>" -w "<value>"
#   Linux:  pass insert kubechecks/<KEY_NAME>
#
# See docs/contributing.md for full setup instructions.

def _get_secret(key_name, required=False):
    """Retrieve a secret from OS keychain, falling back to environment variable."""
    os_name = str(local('uname -s', quiet=True)).strip()
    val = ''
    if os_name == 'Darwin':
        val = str(local(
            'security find-generic-password -a "$USER" -s "kubechecks/%s" -w 2>/dev/null || echo ""' % key_name,
            quiet=True,
        )).strip()
    else:
        # Linux: use pass (password-store)
        val = str(local(
            'pass kubechecks/%s 2>/dev/null || echo ""' % key_name,
            quiet=True,
        )).strip()

    if val == '' and os.getenv(key_name):
        val = os.getenv(key_name)
    if val == '' and required:
        fail('Secret "%s" not found in keychain or environment. See docs/contributing.md for setup instructions.' % key_name)
    return val

# Load .secret file if it exists (backwards compatible)
if os.path.exists('.secret'):
    dotenv(fn='.secret')

# Load secrets from keychain (overrides .secret file values)
_gitlab_token = _get_secret('GITLAB_TOKEN')
_github_token = _get_secret('GITHUB_TOKEN')
_webhook_secret = _get_secret('KUBECHECKS_WEBHOOK_SECRET')
_openai_token = _get_secret('OPENAI_API_TOKEN')
_anthropic_key = _get_secret('ANTHROPIC_API_KEY')

# Make secrets available as env vars for Tilt resources
if _gitlab_token: os.putenv('GITLAB_TOKEN', _gitlab_token)
if _github_token: os.putenv('GITHUB_TOKEN', _github_token)
if _webhook_secret: os.putenv('KUBECHECKS_WEBHOOK_SECRET', _webhook_secret)
if _openai_token: os.putenv('OPENAI_API_TOKEN', _openai_token)
if _anthropic_key: os.putenv('ANTHROPIC_API_KEY', _anthropic_key)

config.define_bool("enable_repo", True, 'create a new project for testing this app')
config.define_string("vcs-type")
config.define_bool("live_debug") # not used, but kept for backwards compat
config.define_string("ngrok_fqdn")
cfg = config.parse()

allow_k8s_contexts([
  'kind-kind',
  'docker-desktop',
  'minikube',
])

k8s_namespace='kubechecks'
namespace_create(k8s_namespace)
k8s_resource(
  objects=[k8s_namespace + ':namespace'],
  labels=["localdev"],
  new_name='k8s:namespace'
)
k8s_context=k8s_context()

# start Tilt with no enabled resources
# config.clear_enabled_resources()

# /////////////////////////////////////////////////////////////////////////////
# N G R O K
# /////////////////////////////////////////////////////////////////////////////

# Load NGROK Tiltfile
load('./localdev/ngrok/Tiltfile', 'deploy_ngrok', 'get_ngrok_url')
deploy_ngrok(cfg)

# /////////////////////////////////////////////////////////////////////////////
# A R G O  C D
# /////////////////////////////////////////////////////////////////////////////

# Load ArgoCD Tiltfile
load('./localdev/argocd/Tiltfile', 'deploy_argo', 'delete_argocd_apps_on_tilt_down', 'force_argocd_cleanup_on_tilt_down')
# make sure apps get removed (cleanly) before ArgoCD is shutdown
delete_argocd_apps_on_tilt_down()
deploy_argo()

#load('./localdev/reloader/Tiltfile', 'deploy_reloader')
#deploy_reloader()

# /////////////////////////////////////////////////////////////////////////////
# T E R R A F O R M
# /////////////////////////////////////////////////////////////////////////////

tfcOutputs=local_terraform_resource(
  'tf-random-pet',
  dir='./localdev/terraform',
  deps=[
    'localdev/terraform/*.tf',
  ],
  labels=["terraform"],
)

kubeProject=""
if cfg.get('enable_repo', True):
  if cfg.get('vcs-type') == 'gitlab':
    check_env_set("GITLAB_TOKEN")

    gitlabOutputs=local_terraform_resource(
      'tf-vcs',
      dir='./localdev/terraform/gitlab',
      env={
        'GITLAB_TOKEN': _gitlab_token,
        'TF_VAR_ngrok_url': get_ngrok_url(cfg),
        'TF_VAR_kubechecks_gitlab_hook_secret_key': _webhook_secret,
      },
      deps=[
        './localdev/terraform/*.tf',
        './localdev/terraform/terraform.tfstate',
        './localdev/terraform/gitlab/*.tf',
      ],
      resource_deps=[
        'tf-random-pet',
        'ngrok'
      ],
      labels=['terraform']
    )
    kubeProject=gitlabOutputs.setdefault('gitlab_project_name', '') if gitlabOutputs else 'foo'
    watch_file('./localdev/terraform/gitlab/project.url')
  else:
    check_env_set("GITHUB_TOKEN")

    githubOutputs=local_terraform_resource(
      'tf-vcs',
      dir='./localdev/terraform/github',
      env={
        'GITHUB_TOKEN': _github_token,
        'TF_VAR_ngrok_url': get_ngrok_url(cfg),
        'TF_VAR_kubechecks_github_hook_secret_key': _webhook_secret,
      },
      deps=[
        './localdev/terraform/*.tf',
        './localdev/terraform/terraform.tfstate',
        './localdev/terraform/github/*.tf',
      ],
      resource_deps=[
        'tf-random-pet',
        'ngrok'
      ],
      labels=['terraform']
    )
    kubeProject=githubOutputs.setdefault('github_repo_name', '') if githubOutputs else 'foo'
    watch_file('./localdev/terraform/github/project.url')

# /////////////////////////////////////////////////////////////////////////////
# K U B E C H E C K S
# /////////////////////////////////////////////////////////////////////////////

test_go(
  'go-test', '.',
  recursive=True,
  timeout='60s',
  extra_args=['-v'],
  labels=["kubechecks"],
  deps=[
    "cmd",
    "pkg",
    "telemetry",
    "main.go",
    "go.mod",
  ],
)



# read .tool-versions file and return a dictionary of tools and their versions
def parse_tool_versions(fn):
    if not os.path.exists(fn):
        warn("tool versions file not found: '%s'" % fn)
        return dict()

    f = read_file(fn)

    lines = str(f).splitlines()

    tools = dict()

    for linenumber in range(len(lines)):
        line = lines[linenumber]
        parts = line.split("#", 1)
        if len(parts) == 2:
            line = parts[0]
        line = line.strip()
        if line == "":
            continue
        parts = line.split(' ', 1)
        tools[parts[0].strip()] = parts[1].strip()
    return tools

tool_versions = parse_tool_versions(".tool-versions")

# get the git commit ref
git_commit = local_output('git rev-parse --short HEAD')
git_tag = local_output('git describe --tags --always --dirty 2>/dev/null || echo "dev"')

# Docker Buildx build via Makefile
# Note: Docker Desktop must have "Use containerd for pulling and storing images" enabled
# in Settings > General so that Kubernetes and Docker share the same image store.
custom_build(
    'kubechecks',
    'make build-debug IMAGE_TAG=$EXPECTED_TAG',
    deps=[
        'cmd',
        'pkg',
        'telemetry',
        'main.go',
        'go.mod',
        'go.sum',
        'Dockerfile',
    ],
    ignore=['./dist', './bin', './plan', './temp'],
    skips_local_docker=True,
)

cmd_button('loc:go mod tidy',
  argv=['go', 'mod', 'tidy'],
  resource='kubechecks',
  icon_name='move_up',
  text='go mod tidy',
)

cmd_button('restart-pod',
   argv=['kubectl', '-n', 'kubechecks', 'rollout', 'restart', 'deployment/kubechecks'],
   resource='kubechecks',
   icon_name='change_circle',
   text='restart pod',
)


# Build helm flags, only including secrets that are actually set
_helm_flags = [
    '--values=./localdev/kubechecks/values.yaml',
    '--set=configMap.env.KUBECHECKS_WEBHOOK_URL_BASE=' + get_ngrok_url(cfg),
    '--set=configMap.env.NGROK_URL=' + get_ngrok_url(cfg),
    '--set=configMap.env.KUBECHECKS_ARGOCD_WEBHOOK_URL=' + get_ngrok_url(cfg) +'/argocd/api/webhook',
    '--set=configMap.env.KUBECHECKS_VCS_TYPE=' + cfg.get('vcs-type', 'gitlab'),
    '--set=secrets.env.KUBECHECKS_VCS_TOKEN=' + (_gitlab_token if 'gitlab' in cfg.get('vcs-type', 'gitlab') else _github_token),
]
if _webhook_secret: _helm_flags.append('--set=secrets.env.KUBECHECKS_WEBHOOK_SECRET=' + _webhook_secret)
if _openai_token:   _helm_flags.append('--set=secrets.env.KUBECHECKS_OPENAI_API_TOKEN=' + _openai_token)
if _anthropic_key:  _helm_flags.append('--set=secrets.env.KUBECHECKS_ANTHROPIC_API_KEY=' + _anthropic_key)

helm_resource(name='kubechecks',
              chart='./charts/kubechecks',
              image_deps=['kubechecks'],
              image_keys=[('deployment.image.name', 'deployment.image.tag')],
              namespace= k8s_namespace,
              flags=_helm_flags,
              labels=["kubechecks"],
              resource_deps=[
                'k8s:namespace',
                'argocd',
                'argocd-crds',
                'tf-vcs' if cfg.get('enable_repo', True) else '',
              ])

k8s_resource(
  'kubechecks',
  port_forwards=['2345:2345', '8080:8080'],
  resource_deps=[
    # 'go-build',
    'go-test',
    'k8s:namespace',
    'argocd',
    'argocd-crds',
  ],
  labels=["kubechecks"]
)

k8s_resource(
    objects=[
      'kubechecks-argocd-application-controller:clusterrole',
      'kubechecks-argocd-server:clusterrole',
      'kubechecks-argocd-application-controller:clusterrolebinding',
      'kubechecks-argocd-server:clusterrolebinding',
    ],
    new_name='kubechecks-rbac',
    resource_deps=['k8s:namespace'],
    labels=["kubechecks"]
)

load("localdev/test_apps/Tiltfile", "install_test_apps")
install_test_apps(cfg)

load("localdev/test_appsets/Tiltfile", "copy_test_appsets")
copy_test_appsets(cfg)


force_argocd_cleanup_on_tilt_down()
