{% set root = '/var/src/build-controller' %}
{% set package = 'github.com/GoogleCloudPlatform/kubernetes' %}
{% set package_dir = root + '/src/' + package %}
{% if grains['os_family'] == 'RedHat' %}
{% set environment_file = '/etc/sysconfig/build-controller' %}
{% else %}
{% set environment_file = '/etc/default/build-controller' %}
{% endif %}

{{ package_dir }}:
  file.recurse:
    - source: salt://build-controller/go
    - user: root
    {% if grains['os_family'] == 'RedHat' %}
    - group: root
    {% else %}
    - group: staff
    {% endif %}
    - dir_mode: 775
    - file_mode: 664
    - makedirs: True
    - recurse:
      - user
      - group
      - mode

build-controller-third-party-go:
  file.recurse:
    - name: {{ root }}/src
    - source: salt://third-party/go/src
    - user: root
    {% if grains['os_family'] == 'RedHat' %}
    - group: root
    {% else %}
    - group: staff
    {% endif %}
    - dir_mode: 775
    - file_mode: 664
    - makedirs: True
    - recurse:
      - user
      - group
      - mode

{{ environment_file }}:
  file.managed:
    - source: salt://build-controller/default
    - template: jinja
    - user: root
    - group: root
    - mode: 644

build-controller-build:
  cmd.wait:
    - cwd: {{ root }}
    - names:
      - go build {{ package }}/cmd/build-controller
    - env:
      - PATH: {{ grains['path'] }}:/usr/local/bin
      - GOPATH: {{ root }}
    - watch:
      - file: {{ package_dir }}

/usr/local/bin/build-controller:
  file.symlink:
    - target: {{ root }}/build-controller
    - watch:
      - cmd: build-controller-build

{% if grains['os_family'] == 'RedHat' %}

/usr/lib/systemd/system/build-controller.service:
  file.managed:
    - source: salt://build-controller/build-controller.service
    - user: root
    - group: root

{% else %}

/etc/init.d/build-controller:
  file.managed:
    - source: salt://build-controller/initd
    - user: root
    - group: root
    - mode: 755

{% endif %}

build-controller:
  group.present:
    - system: True
  user.present:
    - system: True
    - gid_from_name: True
    - shell: /sbin/nologin
    - home: /var/build-controller
    - require:
      - group: build-controller
  service.running:
    - enable: True
    - watch:
      - cmd: build-controller-build
      - file: /usr/local/bin/build-controller
      - file: {{ environment_file }}
{% if grains['os_family'] != 'RedHat' %}
      - file: /etc/init.d/build-controller
{% endif %}


