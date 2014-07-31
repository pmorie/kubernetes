{% set root = '/var/src/deployment-controller' %}
{% set package = 'github.com/GoogleCloudPlatform/kubernetes' %}
{% set package_dir = root + '/src/' + package %}
{% if grains['os_family'] == 'RedHat' %}
{% set environment_file = '/etc/sysconfig/deployment-controller' %}
{% else %}
{% set environment_file = '/etc/default/deployment-controller' %}
{% endif %}

{{ package_dir }}:
  file.recurse:
    - source: salt://deployment-controller/go
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

deployment-controller-third-party-go:
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
    - source: salt://deployment-controller/default
    - template: jinja
    - user: root
    - group: root
    - mode: 644

deployment-controller-build:
  cmd.wait:
    - cwd: {{ root }}
    - names:
      - go build {{ package }}/cmd/deployment-controller
    - env:
      - PATH: {{ grains['path'] }}:/usr/local/bin
      - GOPATH: {{ root }}
    - watch:
      - file: {{ package_dir }}

/usr/local/bin/deployment-controller:
  file.symlink:
    - target: {{ root }}/deployment-controller
    - watch:
      - cmd: deployment-controller-build

{% if grains['os_family'] == 'RedHat' %}

/usr/lib/systemd/system/deployment-controller.service:
  file.managed:
    - source: salt://deployment-controller/deployment-controller.service
    - user: root
    - group: root

{% else %}

/etc/init.d/deployment-controller:
  file.managed:
    - source: salt://deployment-controller/initd
    - user: root
    - group: root
    - mode: 755

{% endif %}

deployment-controller:
  group.present:
    - system: True
  user.present:
    - system: True
    - gid_from_name: True
    - shell: /sbin/nologin
    - home: /var/deployment-controller
    - require:
      - group: deployment-controller
  service.running:
    - enable: True
    - watch:
      - cmd: deployment-controller-build
      - file: /usr/local/bin/deployment-controller
      - file: {{ environment_file }}
{% if grains['os_family'] != 'RedHat' %}
      - file: /etc/init.d/deployment-controller
{% endif %}


