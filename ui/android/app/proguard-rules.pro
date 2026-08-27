# JNI entry points use their declared Kotlin class and method names.
-keepclasseswithmembernames,includedescriptorclasses class com.clambhook.android.** {
    native <methods>;
}

# kotlinx.serialization: keep generated serializers and @Serializable metadata.
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.**
-keepclassmembers @kotlinx.serialization.Serializable class ** {
    *** Companion;
    *** serializer(...);
}
-keepclasseswithmembers class com.clambhook.android.** {
    kotlinx.serialization.KSerializer serializer(...);
}
-keep,includedescriptorclasses class com.clambhook.android.**$$serializer { *; }

# Tink (via androidx.security-crypto) references compile-only errorprone
# annotations that are absent at runtime. Safe to ignore.
-dontwarn com.google.errorprone.annotations.**
