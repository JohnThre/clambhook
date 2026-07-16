project(
  'clambhook-linux',
  ['vala', 'c'],
  version: '0.1.0',
  meson_version: '>=1.0.0',
)

gnome = import('gnome')
cc = meson.get_compiler('c')

add_project_arguments(
  cc.get_supported_arguments([
    '-Wno-unused-variable',
    '-Wno-unused-but-set-variable',
    '-Wno-unused-value',
    '-Wno-unused-function',
  ]),
  language: 'c',
)

gtk_dep = dependency('gtk4')
adw_dep = dependency('libadwaita-1')
gio_dep = dependency('gio-2.0')
glib_dep = dependency('glib-2.0')
gee_dep = dependency('gee-0.8')
json_dep = dependency('json-glib-1.0')
soup_dep = dependency('libsoup-3.0')
secret_dep = dependency('libsecret-1')

app_id = 'com.clambhook.Clambhook'

core_sources = files(
  'src/api-client.vala',
  'src/api-models.vala',
  'src/daemon-supervisor.vala',
  'src/dashboard-store.vala',
  'src/event-stream-client.vala',
  'src/formatters.vala',
  'src/license-service.vala',
  'src/parity-models.vala',
  'src/settings-store.vala',
  'src/token-vault.vala',
)

ui_sources = files(
  'src/clambhook-app.vala',
  'src/main-window.vala',
  'src/license-view.vala',
  'src/parity-views.vala',
  'src/preferences-dialog.vala',
)

resources = gnome.compile_resources(
  'clambhook-linux-resources',
  'data/com.clambhook.Clambhook.gresource.xml',
  source_dir: 'data',
)

deps = [
  gtk_dep,
  adw_dep,
  gio_dep,
  glib_dep,
  gee_dep,
  json_dep,
  soup_dep,
  secret_dep,
]

executable(
  'clambhook-linux',
  core_sources + ui_sources + resources + files('src/main.vala'),
  dependencies: deps,
  install: true,
)

daemon_path = get_option('clambhook_daemon')
if daemon_path != ''
  install_data(
    daemon_path,
    install_dir: get_option('libexecdir'),
    install_mode: 'rwxr-xr-x',
    rename: 'clambhook',
  )
endif

tui_path = get_option('clambhook_tui')
if tui_path != ''
  install_data(
    tui_path,
    install_dir: get_option('bindir'),
    install_mode: 'rwxr-xr-x',
    rename: 'clambhook-tui',
  )
endif

license_path = get_option('clambhook_license')
if license_path != ''
  install_data(
    license_path,
    install_dir: get_option('libexecdir'),
    install_mode: 'rwxr-xr-x',
    rename: 'clambhook-license',
  )
endif

install_data(
  '../../clambhook-icon-1024.png',
  install_dir: get_option('datadir') / 'icons' / 'hicolor' / '1024x1024' / 'apps',
  rename: '@0@.png'.format(app_id),
)

test_exe = executable(
  'clambhook-linux-tests',
  core_sources + files(
    'tests/test-api-client.vala',
    'tests/test-dashboard-store.vala',
    'tests/test-models.vala',
    'tests/test-settings-daemon.vala',
    'tests/test-license-service.vala',
    'tests/test-parity-models.vala',
    'tests/test-main.vala',
  ),
  dependencies: deps,
)

test('clambhook-linux-tests', test_exe)

desktop_conf = configuration_data()
desktop_conf.set('app_id', app_id)
configure_file(
  input: 'data/com.clambhook.Clambhook.desktop.in',
  output: '@0@.desktop'.format(app_id),
  configuration: desktop_conf,
  install: true,
  install_dir: get_option('datadir') / 'applications',
)

configure_file(
  input: 'data/com.clambhook.Clambhook.metainfo.xml.in',
  output: '@0@.metainfo.xml'.format(app_id),
  configuration: desktop_conf,
  install: true,
  install_dir: get_option('datadir') / 'metainfo',
)

# Privileged daemon assets for device-wide TUN routing (native packages only).
install_data(
  '../../packaging/systemd/clambhook-daemon.service',
  install_dir: get_option('prefix') / 'lib' / 'systemd' / 'system',
)

install_data(
  '../../packaging/polkit/com.clambhook.Clambhook.policy',
  install_dir: get_option('datadir') / 'polkit-1' / 'actions',
)
