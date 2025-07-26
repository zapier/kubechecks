load('ext://dotenv', 'dotenv')
load('ext://earthly', 'earthly_build', 'earthly_build_with_restart')
load('ext://helm_remote', 'helm_remote')
load('ext://tests/golang', 'test_go')
load('ext://namespace', 'namespace_create')
load('ext://uibutton', 'cmd_button')
load('ext://helm_resource', 'helm_resource')
load('ext://local_output', 'local_output')
load('./.tilt/terraform/Tiltfile', 'local_terraform_resource')
load('./.tilt/utils/Tiltfile', 'check_env_set')

# Check if the .secret file exists
if not os.path.exists('.secret'):
    fail('The .secret file is missing. Please copy .secret file from .secret.example and setup before running Tilt.')

dotenv(fn='.secret')

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
        'GITLAB_TOKEN': os.getenv('GITLAB_TOKEN'),
        'TF_VAR_ngrok_url': get_ngrok_url(cfg),
        'TF_VAR_kubechecks_gitlab_hook_secret_key': os.getenv('KUBECHECKS_WEBHOOK_SECRET') if os.getenv('KUBECHECKS_WEBHOOK_SECRET') != None else "",

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
        'GITHUB_TOKEN': os.getenv('GITHUB_TOKEN'),
        'TF_VAR_ngrok_url': get_ngrok_url(cfg),
        'TF_VAR_kubechecks_github_hook_secret_key': os.getenv('KUBECHECKS_WEBHOOK_SECRET') if os.getenv('KUBECHECKS_WEBHOOK_SECRET') != None else "",
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

earthly_build(
    context='.',
    target="+docker-debug",
    ref='kubechecks',
    image_arg='IMAGE_NAME',
    ignore='./dist',
    extra_args=[
        '--CHART_RELEASER_VERSION='+tool_versions.get('helm-cr'),
        '--GOLANG_VERSION='+tool_versions.get('golang'),
        '--GOLANGCI_LINT_VERSION='+tool_versions.get('golangci-lint'),
        '--HELM_VERSION='+tool_versions.get('helm'),
        '--KUBECONFORM_VERSION='+tool_versions.get('kubeconform'),
        '--KUSTOMIZE_VERSION='+tool_versions.get('kustomize'),
        '--GIT_COMMIT='+git_commit,
        ],
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


helm_resource(name='kubechecks',
              chart='./charts/kubechecks',
              image_deps=['kubechecks'],
              image_keys=[('deployment.image.name', 'deployment.image.tag')],
              namespace= k8s_namespace,
              flags=[
                '--values=./localdev/kubechecks/values.yaml',
                '--set=configMap.env.KUBECHECKS_WEBHOOK_URL_BASE=' + get_ngrok_url(cfg),
                '--set=configMap.env.NGROK_URL=' + get_ngrok_url(cfg),
                '--set=configMap.env.KUBECHECKS_ARGOCD_WEBHOOK_URL=' + get_ngrok_url(cfg) +'/argocd/api/webhook',
                '--set=configMap.env.KUBECHECKS_VCS_TYPE=' + cfg.get('vcs-type', 'gitlab'),
                '--set=secrets.env.KUBECHECKS_VCS_TOKEN=' + (os.getenv('GITLAB_TOKEN') if 'gitlab' in cfg.get('vcs-type', 'gitlab') else os.getenv('GITHUB_TOKEN')),
                '--set=secrets.env.KUBECHECKS_WEBHOOK_SECRET=' + (os.getenv('KUBECHECKS_WEBHOOK_SECRET') if os.getenv('KUBECHECKS_WEBHOOK_SECRET') != None else ""),
                '--set=secrets.env.KUBECHECKS_OPENAI_API_TOKEN=' + (os.getenv('OPENAI_API_TOKEN') if os.getenv('OPENAI_API_TOKEN') != None else ""),
              ],
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
    # 'go-test',
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
