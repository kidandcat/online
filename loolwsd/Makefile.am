SUBDIRS = . test

export ENABLE_DEBUG

bin_PROGRAMS = loolwsd loolforkit loolmap loolmount looltool

dist_bin_SCRIPTS = loolwsd-systemplate-setup

loolwsddatadir = @LOOLWSD_DATADIR@

loolwsddata_DATA = discovery.xml \
                   robots.txt

loolwsdconfigdir = @LOOLWSD_CONFIGDIR@

loolwsdconfig_DATA = loolwsd.xml \
                     etc/key.pem

if ENABLE_SSL
loolwsdconfig_DATA += etc/cert.pem \
                      etc/ca-chain.cert.pem
endif

ACLOCAL_AMFLAGS = -I m4

AM_CPPFLAGS = -pthread -DLOOLWSD_DATADIR='"@LOOLWSD_DATADIR@"' -DLOOLWSD_CONFIGDIR='"@LOOLWSD_CONFIGDIR@"'
AM_LDFLAGS = -pthread -Wl,-E

AM_ETAGSFLAGS = --c++-kinds=+p --fields=+iaS --extra=+q -R --totals=yes *
AM_CTAGSFLAGS = $(AM_ETAGSFLAGS)

shared_sources = ChildSession.cpp \
                 IoUtil.cpp \
                 Log.cpp \
                 LOOLProtocol.cpp \
                 LOOLSession.cpp \
                 MessageQueue.cpp \
                 Unit.cpp \
                 Util.cpp

loolwsd_SOURCES = Admin.cpp \
                  AdminModel.cpp \
                  Auth.cpp \
                  DocumentBroker.cpp \
                  LOOLWSD.cpp \
                  ClientSession.cpp \
                  FileServer.cpp \
                  PrisonerSession.cpp \
                  Storage.cpp \
                  TileCache.cpp \
                  $(shared_sources)

noinst_PROGRAMS = connect \
                  lokitclient \
                  loolforkit-nocaps

connect_SOURCES = Connect.cpp \
                  Log.cpp \
                  LOOLProtocol.cpp \
                  Util.cpp

lokitclient_SOURCES = IoUtil.cpp \
                      Log.cpp \
                      LOKitClient.cpp \
                      LOOLProtocol.cpp \
                      Util.cpp

loolforkit_SOURCES = LOOLForKit.cpp \
                     LOOLKit.cpp \
                     $(shared_sources)

# build a binary with no caps to help debugging
loolforkit_nocaps_SOURCES = $(loolforkit_SOURCES)

loolmount_SOURCES = loolmount.c

loolmap_SOURCES = loolmap.c

looltool_SOURCES = LOOLTool.cpp

noinst_HEADERS = Admin.hpp \
                 AdminModel.hpp \
                 Auth.hpp \
                 ChildSession.hpp \
                 Common.hpp \
                 DocumentBroker.hpp \
                 Exceptions.hpp \
                 FileServer.hpp \
                 IoUtil.hpp \
                 LibreOfficeKit.hpp \
                 Log.hpp \
                 LOKitHelper.hpp \
                 LOOLKit.hpp \
                 LOOLProtocol.hpp \
                 LOOLSession.hpp \
                 LOOLWSD.hpp \
                 ClientSession.hpp \
                 PrisonerSession.hpp \
                 MessageQueue.hpp \
                 Png.hpp \
                 QueueHandler.hpp \
                 Rectangle.hpp \
                 Storage.hpp \
                 TileCache.hpp \
                 Unit.hpp \
                 UnitHTTP.hpp \
                 UserMessages.hpp \
                 Util.hpp \
                 bundled/include/LibreOfficeKit/LibreOfficeKit.h \
                 bundled/include/LibreOfficeKit/LibreOfficeKitEnums.h \
                 bundled/include/LibreOfficeKit/LibreOfficeKitInit.h \
                 bundled/include/LibreOfficeKit/LibreOfficeKitTypes.h \
                 test/countloolkits.hpp \
                 test/helpers.hpp \
                 security.h

EXTRA_DIST = discovery.xml \
             loolwsd.xml.in \
             loolwsd.service \
             robots.txt \
             sysconfig.loolwsd \
             etc/key.pem \
             etc/cert.pem \
             etc/ca-chain.cert.pem

if HAVE_LO_PATH

SYSTEM_STAMP = @SYSTEMPLATE_PATH@/system_stamp

$(SYSTEM_STAMP) : ${top_srcdir}/loolwsd-systemplate-setup
	if test "z@SYSTEMPLATE_PATH@" != "z"; then rm -rf "@SYSTEMPLATE_PATH@"; fi
	${top_srcdir}/loolwsd-systemplate-setup "@SYSTEMPLATE_PATH@" "@LO_PATH@" && touch $@

@JAILS_PATH@ :
	mkdir -p $@

clean-local:
	if test "z@JAILS_PATH@" != "z"; then rm -rf "@JAILS_PATH@"; fi
	if test "z@SYSTEMPLATE_PATH@" != "z"; then rm -rf "@SYSTEMPLATE_PATH@"; fi

run: all @JAILS_PATH@ @SYSTEMPLATE_PATH@/system_stamp
	@echo "Launching loolwsd"
	@cp $(abs_top_srcdir)/test/data/hello.odt $(abs_top_srcdir)/test/data/hello-world.odt
	@echo
	./loolwsd --o:sys_template_path="@SYSTEMPLATE_PATH@" --o:lo_template_path="@LO_PATH@" \
			  --o:child_root_path="@JAILS_PATH@" --o:storage.filesystem[@allow]=true \
			  --o:admin_console.username=admin --o:admin_console.password=admin

run_valgrind: all @JAILS_PATH@ @SYSTEMPLATE_PATH@/system_stamp
	@echo "Launching loolwsd under valgrind (but not forkit/loolkit, yet)"
	valgrind --tool=memcheck --trace-children=no -v --read-var-info=yes \
		./loolwsd --o:sys_template_path="@SYSTEMPLATE_PATH@" --o:lo_template_path="@LO_PATH@" \
			  --o:child_root_path="@JAILS_PATH@" --o:storage.filesystem[@allow]=true \
			  --o:admin_console.username=admin --o:admin_console.password=admin
else

SYSTEM_STAMP =

endif

clean-cache cache-clean:
# Intentionally don't use "*" below... Avoid risk of accidentally running rm -rf /*
	test -n "@LOOLWSD_CACHEDIR@" && rm -rf "@LOOLWSD_CACHEDIR@"/[0-9a-f]

# After building loolforkit, set its capabilities as required. Do it
# already after a plain 'make' to allow for testing without
# installing. When building for packaging, no need for this, as the
# capabilities won't survive packaging anyway. Instead, handle it when
# installing the RPM or Debian package.

all-local: loolforkit @JAILS_PATH@ $(SYSTEM_STAMP)
	if test "$$BUILDING_FROM_RPMBUILD" != yes; then \
	    sudo @SETCAP@ cap_fowner,cap_mknod,cap_sys_chroot=ep loolforkit; \
	    sudo @SETCAP@ cap_sys_admin=ep loolmount; \
	    echo "Set required capabilities"; \
	else \
	    echo "Skipping capability setting"; \
	fi
