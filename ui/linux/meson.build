project(
  'clambhook-linux',
  ['vala', 'c'],
  version: '0.1.0',
  meson_version: '>=1.0.0',
)

gnome = import('gnome')

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
  'src/formatters.vala',
  'src/settings-store.vala',
  'src/token-vault.vala',
)

ui_sources = files(
  'src/clambhook-app.vala',
  'src/main-window.vala',
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

test_exe = executable(
  'clambhook-linux-tests',
  core_sources + files(
    'tests/test-api-client.vala',
    'tests/test-dashboard-store.vala',
    'tests/test-models.vala',
    'tests/test-settings-daemon.vala',
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
