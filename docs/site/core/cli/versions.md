# Версии plugins и releases сайтов

В v1 Core не устанавливает, не обновляет и не откатывает plugin binaries. Их
версии/процессы обслуживает оператор, а Core показывает обнаруженные Manifest и
release digest рядом с active/previous config generations. Произвольные
plugin artifacts API Core не принимает.

Site releases принадлежат Server plugin, а не Core registry. Plugin хранит
immutable artifact releases и pointers `current/previous` на своём persistent
filesystem и меняет их через собственную Admin Surface. API details — в
[plugin configuration](../api/config) и
[целевой архитектуре](../architecture/target).
