{% set root = '/var/src/job-controller' %}
{% set package = 'github.com/GoogleCloudPlatform/kubernetes' %}
{% set package_dir = root + '/src/' + package %}
{% if grains['os_family'] == 'RedHat' %}
{% set environment_file = '/etc/sysconfig/job-controller' %}
{% else %}
{% set environment_file = '/etc/default/job-controller' %}
{% endif %}

{{ package_dir }}:
  file.recurse:
    - source: salt://job-controller/go
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

job-controller-third-party-go:
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
    - source: salt://job-controller/default
    - template: jinja
    - user: root
    - group: root
    - mode: 644

job-controller-build:
  cmd.wait:
    - cwd: {{ root }}
    - names:
      - go build {{ package }}/cmd/job-controller
    - env:
      - PATH: {{ grains['path'] }}:/usr/local/bin
      - GOPATH: {{ root }}
    - watch:
      - file: {{ package_dir }}

/usr/local/bin/job-controller:
  file.symlink:
    - target: {{ root }}/job-controller
    - watch:
      - cmd: job-controller-build

{% if grains['os_family'] == 'RedHat' %}

/usr/lib/systemd/system/job-controller.service:
  file.managed:
    - source: salt://job-controller/job-controller.service
    - user: root
    - group: root

{% else %}

/etc/init.d/job-controller:
  file.managed:
    - source: salt://job-controller/initd
    - user: root
    - group: root
    - mode: 755

{% endif %}

job-controller:
  group.present:
    - system: True
  user.present:
    - system: True
    - gid_from_name: True
    - shell: /sbin/nologin
    - home: /var/job-controller
    - require:
      - group: job-controller
  service.running:
    - enable: True
    - watch:
      - cmd: job-controller-build
      - file: /usr/local/bin/job-controller
      - file: {{ environment_file }}
{% if grains['os_family'] != 'RedHat' %}
      - file: /etc/init.d/job-controller
{% endif %}


