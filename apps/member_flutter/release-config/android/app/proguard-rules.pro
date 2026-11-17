# ProGuard/R8 rules for the PFAEngine member app (release builds).
# Applied via android/app/build.gradle (minifyEnabled + shrinkResources).

# --- Flutter engine & embedding -------------------------------------------
-keep class io.flutter.app.** { *; }
-keep class io.flutter.plugin.** { *; }
-keep class io.flutter.util.** { *; }
-keep class io.flutter.view.** { *; }
-keep class io.flutter.** { *; }
-keep class io.flutter.plugins.** { *; }
-dontwarn io.flutter.embedding.**

# --- Plugins used by this app ----------------------------------------------
# camera (selfie-video liveness capture)
-keep class io.flutter.plugins.camera.** { *; }
-dontwarn io.flutter.plugins.camera.**
# flutter_secure_storage
-keep class com.it_nomads.fluttersecurestorage.** { *; }

# --- Dart wrappers are stripped by the AOT compiler anyway; keep names used
# --- via platform channels reflective lookups conservative:
-keepclassmembers class * {
    @io.flutter.plugin.common.* <methods>;
}

# Keep model classes if/when JSON (de)serialization via reflection is added.
# (http package uses no reflection; add rules here if json_serializable or
# similar is introduced.)
