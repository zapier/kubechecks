load('ext://configmap', 'configmap_from_dict')
load('ext://dotenv', 'dotenv')
load('ext://tests/golang', 'test_go')
load('ext://list_port_forwards', 'display_port_forwards')
load('ext://namespace', 'namespace_yaml')
load('ext://restart_process', 'docker_build_with_restart')
load('ext://secret', 'secret_from_dict')
load('ext://uibutton', 'cmd_button')
load('./.tilt/terraform/Tiltfile', 'local_terraform_resource')
load('./.tilt/utils/Tiltfile', 'check_env_set')
dotenv()

config.define_bool("enable_repo", True, 'create a new project for testing this app')
config.define_string("vcs-type")
config.define_bool("live_debug")
config.define_string("ngrok_fqdn")
cfg = config.parse()

allow_k8s_contexts([
  'kind-kind',
  'docker-desktop',
  'minikube',
])

k8s_namespace='kubechecks'
k8s_yaml(namespace_yaml(k8s_namespace), allow_duplicates=False)
k8s_resource(
  objects=['kubechecks:namespace'],
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
load('./localdev/argocd/Tiltfile', 'deploy_argo', 'cleanup_argo_apps')
deploy_argo()
if config.tilt_subcommand == 'down':
    cleanup_argo_apps(k8s_context, k8s_namespace)

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
      'tf-gitlab',
      dir='./localdev/terraform/gitlab',
      env={
        'GITLAB_TOKEN': os.getenv('GITLAB_TOKEN'),
        'TF_VAR_ngrok_url': get_ngrok_url(cfg),
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
      'tf-github',
      dir='./localdev/terraform/github',
      env={
        'GITHUB_TOKEN': os.getenv('GITHUB_TOKEN'),
        'TF_VAR_ngrok_url': get_ngrok_url(cfg),
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
  'go-test', '.', '.',
  recursive=True,
  timeout='30s',
  extra_args=['-v'],
  labels=["kubechecks"],
  ignore=[
    'localdev/*',
  ],
)

arch="arm64" if str(local("uname -m")).strip('\n') == "arm64" else "amd64"
build_cmd = """
CGO_ENABLED=0 GOOS=linux GOARCH={} go build -gcflags="all=-N -l" \
   -ldflags "-X github.com/zapier/kubechecks/pkg.GitCommit={}" \
  -o build/kubechecks ./
"""
local_resource(
  'go-build',
  build_cmd.format(arch, "dev"),
  deps=[
    './main.go',
    './go.mod',
    './go.sum',
    './cmd',
    './internal',
    './pkg',
    './controller',
  ],
  labels=["kubechecks"],
  resource_deps = ['go-test']
)

if cfg.get("live_debug"):
  docker_build_with_restart(
    'kubechecks-server',
    '.',
    dockerfile='localdev/Dockerfile.dlv',
    entrypoint='$GOPATH/bin/dlv --listen=:2345 --api-version=2 --headless=true --accept-multiclient exec --continue /app/kubechecks controller',
    ignore=['./Dockerfile', '.git'],
    only=[
      './build',
      './policy',
      './schemas'
    ],
    live_update=[
        sync('./build/kubechecks', '/app/kubechecks'),
    ]
  )

else:
  docker_build(
    'kubechecks-server',
    '.',
    dockerfile='localdev/Dockerfile',
    only=[
      './build',
      './policy',
      './schemas'
    ]
  )

cmd_button('loc:go mod tidy',
  argv=['go', 'mod', 'tidy'],
  resource='kubechecks',
  icon_name='move_up',
  text='go mod tidy',
)
cmd_button('generate-mocks',
   argv=['go', 'generate', './...'],
   resource='kubechecks',
   icon_name='change_circle',
   text='go generate',
)
cmd_button('restart-pod',
   argv=['kubectl', '-n', 'kubechecks', 'rollout', 'restart', 'deployment/kubechecks'],
   resource='kubechecks',
   icon_name='change_circle',
   text='restart pod',
)

k8s_yaml(helm(
  './charts/kubechecks/',
  namespace='kubechecks',
  name='kubechecks',
  values='./localdev/kubechecks/values.yaml',
  set=['deployment.env.KUBECHECKS_WEBHOOK_URL_BASE=' + get_ngrok_url(cfg), 'deployment.env.NGROK_URL=' + get_ngrok_url(cfg),
        'deployment.env.KUBECHECKS_ARGOCD_WEBHOOK_URL='+ get_ngrok_url(cfg) +'/argocd/api/webhook',
        'deployment.env.KUBECHECKS_VCS_TYPE=' + cfg.get('vcs-type', 'gitlab'),
        'secrets.env.KUBECHECKS_VCS_TOKEN=' + (os.getenv('GITLAB_TOKEN') if 'gitlab' in cfg.get('vcs-type', 'gitlab') else os.getenv('GITHUB_TOKEN')),
        'secrets.env.KUBECHECKS_GITHUB_HOOK_SECRET_KEY=' + (os.getenv('KUBECHECKS_HOOK_SECRET') if os.getenv('KUBECHECKS_HOOK_SECRET') != None else ""),
        'secrets.env.KUBECHECKS_GITLAB_HOOK_SECRET_KEY=' + (os.getenv('KUBECHECKS_HOOK_SECRET') if os.getenv('KUBECHECKS_HOOK_SECRET') != None else ""),
        'secrets.env.KUBECHECKS_OPENAI_API_TOKEN=' + (os.getenv('OPENAI_API_TOKEN') if os.getenv('OPENAI_API_TOKEN') != None else ""),],
))

k8s_resource(
  'kubechecks',
  port_forwards=['2345:2345', '8080:8080'],
  resource_deps=[
    # 'go-build',
    'go-test',
    'k8s:namespace'
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
    labels=["kubechecks"],
    resource_deps=['k8s:namespace']
)

# /////////////////////////////////////////////////////////////////////////////
# Test Apps
# /////////////////////////////////////////////////////////////////////////////
# Load the terraform url we output, default to gitlab if cant find a vcs-type variable
vcsPath = "./localdev/terraform/{}/project.url".format(cfg.get('vcs-type', 'gitlab'))
print("Path to url: " + vcsPath)
projectUrl=str(read_file(vcsPath, "")).strip('\n')
print("Remote Project URL: " + projectUrl)

if projectUrl != "":
  for app in ["echo-server", "httpbin"]:
    print("Creating Test App: " + app)
    # apply the test Application manifests to the test namespace
    # update the source repo URL with our test gitlab project
    apply_cmd = """
    envsubst < {}.yaml | kubectl -n {} apply -f - 1>&2
    kubectl --namespace={} get application in-cluster-{} -oyaml 
    """
    k8s_custom_deploy (
        '{}-application'.format(app) ,
        apply_cmd.format(app, k8s_namespace, k8s_namespace, app),
        'kubectl -n {} delete application in-cluster-{} --wait || true'.format(k8s_namespace, app),
        [
            'localdev/test_apps/{}.yaml'.format(app),
        ] ,
        apply_dir = 'localdev/test_apps/',
        apply_env = {
            "REPO_URL": projectUrl,
        } ,
        delete_dir = 'localdev/test_apps/',
        delete_env = {},
    )

  for appset in ["httpdump"]:
    print("Creating Test Appsets: " + app)
    # apply the test Application manifests to the test namespace
    # update the source repo URL with our test gitlab project
    apply_cmd = """
    envsubst < {}.yaml | kubectl -n {} apply -f - 1>&2
    kubectl get applicationset {} -oyaml --namespace={}
    """
    k8s_custom_deploy (
        '{}-applicationset'.format(appset) ,
        apply_cmd.format(appset, k8s_namespace, appset, k8s_namespace),
        'kubectl -n {} delete applicationset {} --wait || true'.format(k8s_namespace, appset),
        [
            'localdev/test_appsets/{}.yaml'.format(appset),
        ] ,
        apply_dir = 'localdev/test_appsets/',
        apply_env = {
            "REPO_URL": projectUrl,
        } ,
        delete_dir = 'localdev/test_appsets/',
        delete_env = {},
    )

display_port_forwards()